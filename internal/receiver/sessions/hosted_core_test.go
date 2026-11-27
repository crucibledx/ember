package sessions

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/extensiontest"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

func TestChangedComparesModTimeInstants(t *testing.T) {
	home := t.TempDir()
	name := filepath.Join(home, "run.json")
	if err := os.WriteFile(name, []byte("run"), 0o600); err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 2, 3, 4, 5, 6, 700, time.FixedZone("test", -7*60*60))
	if err := os.Chtimes(name, instant, instant); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{run: harness.Run{Harness: "fake", SessionID: "run"}}
	c := &core{userHome: home, state: emptyState()}
	c.state.Runs[stateKey("fake", "run")] = &runState{
		Run:   a.run,
		Files: map[string]fileState{"run.json": {Size: info.Size(), ModTime: instant.UTC()}},
	}
	if c.changed(a, harness.SessionRef{RunID: "run", Files: []string{"run.json"}}) {
		t.Fatal("equal modtime instants with different locations were treated as a change")
	}
}

type mutatingParseAdapter struct {
	home     string
	run      harness.Run
	mutation string
	parses   int
}

func (a *mutatingParseAdapter) Name() string                                             { return "mutating" }
func (a *mutatingParseAdapter) Detect(harness.Env) harness.Detection                     { return harness.Detection{} }
func (a *mutatingParseAdapter) Setup(harness.Env, harness.SetupOptions) []harness.Change { return nil }
func (a *mutatingParseAdapter) Sessions(harness.Env) ([]harness.SessionRef, error) {
	return []harness.SessionRef{{RunID: a.run.SessionID, Files: []string{"run.json"}}}, nil
}
func (a *mutatingParseAdapter) Parse(fs.FS, harness.SessionRef) (harness.Run, error) {
	a.parses++
	if a.parses == 1 {
		if err := os.WriteFile(filepath.Join(a.home, "run.json"), []byte(a.mutation), 0o600); err != nil {
			return harness.Run{}, err
		}
	}
	return a.run, nil
}
func (a *mutatingParseAdapter) Mapping() harness.Mapping { return harness.Mapping{} }

func TestWriteDuringParseRemainsEligible(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		name := "poll"
		if resolve {
			name = "resolve"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "run.json"), []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			a := &mutatingParseAdapter{
				home:     home,
				mutation: "written during parse",
				run: harness.Run{
					Harness: "mutating", SessionID: "run", Status: harness.StatusRunning,
					StartedAt: start, TurnTimes: []time.Time{start},
				},
			}
			c := newCore(createDefaultConfig().(*Config), []harness.Adapter{a})
			c.registry = registry.New()
			c.userHome = home
			if err := c.start(home); err != nil {
				t.Fatal(err)
			}
			parse := func() error { return c.Poll(context.Background()) }
			if resolve {
				parse = func() error {
					return c.resolveRun(registry.Key{Harness: "mutating", RunID: "run"})
				}
			}
			if err := parse(); err != nil {
				t.Fatal(err)
			}
			ref := harness.SessionRef{RunID: "run", Files: []string{"run.json"}}
			if !c.changed(a, ref) {
				t.Fatal("write during parse was incorrectly checkpointed")
			}
			if err := parse(); err != nil {
				t.Fatal(err)
			}
			if a.parses != 2 || c.changed(a, ref) {
				t.Fatalf("follow-up parse did not checkpoint: parses=%d", a.parses)
			}
		})
	}
}

func TestNewActivityInvalidatesFinalMetrics(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		name := "poll"
		if resolve {
			name = "resolve"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, harness.StatusCompleted)
			f.poll(t)
			id := stateKey("fake", "run")
			if !f.core.state.Runs[id].FinalMetrics {
				t.Fatal("initial final metrics were not checkpointed")
			}
			f.core.metrics = nil
			f.adapter.run.Status = harness.StatusRunning
			f.adapter.run.TurnTimes = append(f.adapter.run.TurnTimes, f.adapter.run.StartedAt.Add(2*time.Minute))
			if resolve {
				if err := f.core.resolveRun(registry.Key{Harness: "fake", RunID: "run"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(f.home, "run.json"), []byte("resumed"), 0o600); err != nil {
					t.Fatal(err)
				}
				f.poll(t)
			}
			if f.core.state.Runs[id].FinalMetrics {
				t.Fatal("new activity retained the old final metric checkpoint")
			}
		})
	}
}

func TestUnchangedParsePreservesFinalMetrics(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	f.poll(t)
	f.core.metrics = nil
	if err := os.WriteFile(filepath.Join(f.home, "run.json"), []byte("same run"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
		t.Fatal("unchanged parse cleared the final metric checkpoint")
	}
}

func TestRegistryOwnerPersistsAcrossReparseAndRestart(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	f.poll(t)
	key := registry.Key{Harness: "fake", RunID: "run"}
	f.core.registry.NoteOwner(key, "native-owner")
	f.adapter.run.ToolCalls = append(f.adapter.run.ToolCalls, harness.ToolCall{
		ID: "after-owner", Name: "tool", StartedAt: f.adapter.run.StartedAt.Add(2 * time.Minute),
		EndedAt: f.adapter.run.StartedAt.Add(3 * time.Minute),
	})
	if err := os.WriteFile(filepath.Join(f.home, "run.json"), []byte("new activity"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if got := f.core.state.Runs[stateKey("fake", "run")].Run.Owner; got != "native-owner" {
		t.Fatalf("persisted owner=%q", got)
	}
	found := false
	for _, metrics := range f.metrics.AllMetrics() {
		for _, resource := range metrics.ResourceMetrics().All() {
			for _, scope := range resource.ScopeMetrics().All() {
				for _, metric := range scope.Metrics().All() {
					if metric.Name() != "ember.lineage" {
						continue
					}
					for _, point := range metric.Gauge().DataPoints().All() {
						if owner, ok := point.Attributes().Get("ember.human.started_by"); ok && owner.Str() == "fake:native-owner" {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("emitted metrics did not inherit the native owner")
	}
	f.core = f.newCore()
	f.poll(t)
	snapshot, ok := f.core.registry.Get(key)
	if !ok || snapshot.Run.Owner != "native-owner" || snapshot.StartedBy != "fake:native-owner" {
		t.Fatalf("owner was not restored after restart: %+v", snapshot)
	}
}

func TestLoadStateRejectsZeroStartedAt(t *testing.T) {
	home := t.TempDir()
	state := emptyState()
	state.Runs[stateKey("fake", "run")] = &runState{
		Run: harness.Run{Harness: "fake", SessionID: "run", Status: harness.StatusRunning},
	}
	if err := saveState(home, state); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(home); err == nil {
		t.Fatal("valid JSON with a zero run start was accepted")
	}
}

func TestReceiverRepeatedStartKeepsConsumers(t *testing.T) {
	ctx := context.Background()
	extensionFactory := registry.NewFactory()
	id := component.MustNewID("ember")
	ext, err := extensionFactory.Create(ctx, extensiontest.NewNopSettings(id.Type()), extensionFactory.CreateDefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	host := registryHost{extensions: map[component.ID]component.Component{id: ext}}
	if err = ext.Start(ctx, host); err != nil {
		t.Fatal(err)
	}
	cfg := createDefaultConfig().(*Config)
	cfg.Home = t.TempDir()
	cfg.Poll = time.Hour
	r := newSessionReceiver(cfg, nil)
	r.core.adapters = []harness.Adapter{}
	first, second := &consumertest.TracesSink{}, &consumertest.TracesSink{}
	r.traces = first
	if err = r.Start(ctx, host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Shutdown(ctx)
		_ = r.Shutdown(ctx)
		_ = ext.Shutdown(ctx)
	})
	r.traces = second
	if err = r.Start(ctx, host); err != nil {
		t.Fatal(err)
	}
	if r.core.traces != first {
		t.Fatal("repeated Start replaced the active polling consumer")
	}
}
