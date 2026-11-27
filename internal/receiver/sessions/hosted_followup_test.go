package sessions

import (
	"io/fs"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

func TestNativeOwnerSurvivesChangedFileRestart(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	f.poll(t)
	key := registry.Key{Harness: "fake", RunID: "run"}
	f.core.registry.NoteOwner(key, "native-owner")
	f.poll(t)
	f.core = f.newCore()
	f.adapter.run.ToolCalls = append(f.adapter.run.ToolCalls, harness.ToolCall{
		ID: "after-restart", Name: "tool", StartedAt: f.adapter.run.StartedAt.Add(2 * time.Minute),
		EndedAt: f.adapter.run.StartedAt.Add(3 * time.Minute),
	})
	if err := os.WriteFile(f.home+"/run.json", []byte("changed before first restart poll"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if got := f.core.state.Runs[stateKey("fake", "run")].Run.Owner; got != "native-owner" {
		t.Fatalf("owner after changed-file restart = %q", got)
	}
	snapshot, ok := f.core.registry.Get(key)
	if !ok || snapshot.StartedBy != "fake:native-owner" {
		t.Fatalf("owner attribution after changed-file restart = %+v", snapshot)
	}
}

func TestUsageOnlyChangeInvalidatesFinalMetrics(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	f.poll(t)
	if !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
		t.Fatal("initial closing metrics were not checkpointed")
	}
	f.resetCounts()
	f.adapter.run.Usage = map[string]harness.Usage{"model": {Input: 10}}
	if err := os.WriteFile(f.home+"/run.json", []byte("settings-only usage update"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if f.metrics.DataPointCount() == 0 {
		t.Fatal("usage-only change was suppressed by the old final checkpoint")
	}
	found := false
	for _, batch := range f.metrics.AllMetrics() {
		for _, resource := range batch.ResourceMetrics().All() {
			for _, scope := range resource.ScopeMetrics().All() {
				for _, metric := range scope.Metrics().All() {
					if metric.Type() != pmetric.MetricTypeSum {
						continue
					}
					for _, point := range metric.Sum().DataPoints().All() {
						kind, _ := point.Attributes().Get("gen_ai.token.type")
						if kind.Str() == "input" && point.IntValue() == 10 {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("corrected usage was not emitted")
	}
	f.resetCounts()
	f.poll(t)
	if got := f.metrics.DataPointCount(); got != 0 {
		t.Fatalf("unchanged reparse repeated final metrics: %d", got)
	}
}

func TestRegistryParentUnchangedReparseDoesNotRepeatFinalMetrics(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	key := registry.Key{Harness: "fake", RunID: "run"}
	f.core.registry.RecordParent(key, registry.Key{Harness: "fake", RunID: "parent"}, "spawn")
	f.poll(t)
	f.resetCounts()
	if err := os.WriteFile(f.home+"/run.json", []byte("same authoritative run"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if got := f.metrics.DataPointCount(); got != 0 {
		t.Fatalf("registry-parent overlay repeated closing metrics: %d", got)
	}
}

type lateChildAdapter struct {
	*fakeAdapter
	child        harness.Run
	includeChild bool
}

func (a *lateChildAdapter) Sessions(env harness.Env) ([]harness.SessionRef, error) {
	refs, err := a.fakeAdapter.Sessions(env)
	if a.includeChild {
		refs = append(refs, harness.SessionRef{RunID: a.child.SessionID, Files: []string{"run.json"}})
	}
	return refs, err
}
func (a *lateChildAdapter) Parse(files fs.FS, ref harness.SessionRef) (harness.Run, error) {
	if ref.RunID == a.child.SessionID {
		return a.child, nil
	}
	return a.fakeAdapter.Parse(files, ref)
}

func TestLateChildInvalidatesParentFinalMetrics(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	adapter := &lateChildAdapter{fakeAdapter: f.adapter, child: harness.Run{
		Harness: "fake", SessionID: "child", Status: harness.StatusCompleted,
		StartedAt: f.adapter.run.StartedAt.Add(time.Minute), EndedAt: f.adapter.run.StartedAt.Add(2 * time.Minute),
		Parent: &harness.ParentRef{SessionID: "run", ToolCallID: "spawn"},
	}}
	f.core.adapters = []harness.Adapter{adapter}
	f.poll(t)
	f.resetCounts()
	adapter.includeChild = true
	f.poll(t)
	found := false
	for _, batch := range f.metrics.AllMetrics() {
		for _, resource := range batch.ResourceMetrics().All() {
			for _, scope := range resource.ScopeMetrics().All() {
				for _, metric := range scope.Metrics().All() {
					if metric.Name() != "ember.lineage" || metric.Type() != pmetric.MetricTypeGauge {
						continue
					}
					for _, point := range metric.Gauge().DataPoints().All() {
						id, _ := point.Attributes().Get("session.id")
						children, _ := point.Attributes().Get("ember.agent.children")
						if id.Str() == "run" && children.Int() == 1 {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("late child did not correct the closed parent's child count")
	}
}
