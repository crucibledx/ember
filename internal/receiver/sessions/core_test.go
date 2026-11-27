package sessions

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

type fakeAdapter struct {
	run    harness.Run
	parses atomic.Int64
}

func (a *fakeAdapter) Name() string                                             { return "fake" }
func (a *fakeAdapter) Detect(harness.Env) harness.Detection                     { return harness.Detection{} }
func (a *fakeAdapter) Setup(harness.Env, harness.SetupOptions) []harness.Change { return nil }
func (a *fakeAdapter) Sessions(harness.Env) ([]harness.SessionRef, error) {
	return []harness.SessionRef{{RunID: a.run.SessionID, Files: []string{"run.json"}}}, nil
}
func (a *fakeAdapter) Parse(fs.FS, harness.SessionRef) (harness.Run, error) {
	a.parses.Add(1)
	return a.run, nil
}
func (a *fakeAdapter) Mapping() harness.Mapping { return harness.Mapping{} }

type fixture struct {
	home    string
	adapter *fakeAdapter
	core    *core
	traces  *consumertest.TracesSink
	logs    *consumertest.LogsSink
	metrics *consumertest.MetricsSink
}

func newFixture(t *testing.T, status harness.RunStatus) *fixture {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(home+"/run.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &fakeAdapter{run: harness.Run{Harness: "fake", SessionID: "run", Kind: harness.KindAgent, Status: status, StartedAt: start, EndedAt: start.Add(time.Minute), TurnTimes: []time.Time{start.Add(time.Second)}, ToolCalls: []harness.ToolCall{{ID: "call", Name: "tool", StartedAt: start.Add(2 * time.Second), EndedAt: start.Add(3 * time.Second)}}}}
	f := &fixture{home: home, adapter: a, traces: &consumertest.TracesSink{}, logs: &consumertest.LogsSink{}, metrics: &consumertest.MetricsSink{}}
	f.core = f.newCore()
	return f
}
func (f *fixture) newCore() *core {
	c := newCore(f.coreConfig(), []harness.Adapter{f.adapter})
	c.registry = registry.New()
	c.traces = f.traces
	c.logs = f.logs
	c.metrics = f.metrics
	c.now = func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }
	if err := c.start(f.home); err != nil {
		panic(err)
	}
	return c
}
func (f *fixture) coreConfig() *Config {
	c := createDefaultConfig().(*Config)
	c.Home = f.home
	c.Poll = time.Hour
	return c
}
func (f *fixture) poll(t *testing.T) {
	t.Helper()
	if err := f.core.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) resetCounts() { f.traces.Reset(); f.logs.Reset(); f.metrics.Reset() }

func TestEmitOnce(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.poll(t)
		if got := f.adapter.parses.Load(); got != 1 {
			t.Fatalf("parses=%d", got)
		}
		f.resetCounts()
		f.poll(t)
		if f.adapter.parses.Load() != 1 || f.traces.SpanCount() != 0 || f.logs.LogRecordCount() != 0 || f.metrics.DataPointCount() != 0 {
			t.Fatal("unchanged run emitted or parsed again")
		}
	})
	t.Run("changed once", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.poll(t)
		f.resetCounts()
		f.adapter.run.ToolCalls = append(f.adapter.run.ToolCalls, harness.ToolCall{ID: "new", Name: "tool", StartedAt: f.adapter.run.StartedAt.Add(4 * time.Second), EndedAt: f.adapter.run.StartedAt.Add(5 * time.Second)})
		if err := os.WriteFile(f.home+"/run.json", []byte("{ }"), 0o600); err != nil {
			t.Fatal(err)
		}
		f.poll(t)
		if f.adapter.parses.Load() != 2 || f.traces.SpanCount() != 1 {
			t.Fatalf("parses=%d spans=%d", f.adapter.parses.Load(), f.traces.SpanCount())
		}
	})
	t.Run("consumer retry", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.core.traces = consumertest.NewErr(errors.New("retry"))
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want consumer error")
		}
		f.core.traces = f.traces
		f.poll(t)
		if f.traces.SpanCount() != 2 {
			t.Fatalf("spans=%d", f.traces.SpanCount())
		}
		f.resetCounts()
		f.poll(t)
		if f.traces.SpanCount() != 0 {
			t.Fatal("duplicate after retry")
		}
	})
	t.Run("restart", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.poll(t)
		f.resetCounts()
		f.core = f.newCore()
		f.poll(t)
		if f.adapter.parses.Load() != 1 || f.traces.SpanCount() != 0 || f.logs.LogRecordCount() != 0 {
			t.Fatal("restart re-parsed or duplicated")
		}
	})
	t.Run("rescan", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.poll(t)
		f.resetCounts()
		config := f.coreConfig()
		config.Rescan = 24 * time.Hour
		c := newCore(config, []harness.Adapter{f.adapter})
		c.registry = registry.New()
		c.traces = f.traces
		c.logs = f.logs
		c.metrics = f.metrics
		c.now = f.core.now
		if err := c.start(f.home); err != nil {
			t.Fatal(err)
		}
		f.core = c
		f.poll(t)
		if f.traces.SpanCount() == 0 || f.logs.LogRecordCount() == 0 {
			t.Fatal("rescan did not re-emit")
		}
	})
	t.Run("equal time logs", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		at := f.adapter.run.StartedAt.Add(time.Second)
		f.adapter.run.TurnTimes = []time.Time{at, at}
		f.poll(t)
		if got := f.logs.LogRecordCount(); got != 5 {
			t.Fatalf("logs=%d", got)
		}
		f.resetCounts()
		f.poll(t)
		if f.logs.LogRecordCount() != 0 {
			t.Fatal("equal timestamp logs duplicated")
		}
	})
	t.Run("metrics active and final", func(t *testing.T) {
		f := newFixture(t, harness.StatusRunning)
		f.core.config.Idle = 2 * time.Hour
		f.poll(t)
		f.resetCounts()
		f.poll(t)
		if f.metrics.DataPointCount() == 0 {
			t.Fatal("active metrics not repeated")
		}
		f.adapter.run.Status = harness.StatusCompleted
		if err := os.WriteFile(f.home+"/run.json", []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		f.poll(t)
		f.resetCounts()
		f.poll(t)
		if f.metrics.DataPointCount() != 0 {
			t.Fatal("final metrics repeated")
		}
	})
	t.Run("corrupt state", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.poll(t)
		if err := os.WriteFile(f.home+"/state/sessions.json", []byte("bad"), 0o600); err != nil {
			t.Fatal(err)
		}
		f.core = f.newCore()
		f.poll(t)
		if f.adapter.parses.Load() != 2 {
			t.Fatal("corrupt state was not recovered")
		}
	})
}

func TestNativeFirst(t *testing.T) {
	t.Run("exact cutoff", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		key := registry.Key{Harness: "fake", RunID: "run"}
		first := f.core.now().Add(-time.Minute)
		f.core.registry.NoteNative(key, first)
		f.poll(t)
		if f.traces.SpanCount() != 2 {
			t.Fatalf("before grace spans=%d", f.traces.SpanCount())
		}
		f.resetCounts()
		f.core.now = func() time.Time { return first.Add(2 * time.Minute) }
		f.adapter.run.ToolCalls = append(f.adapter.run.ToolCalls, harness.ToolCall{ID: "late", Name: "tool", StartedAt: first, EndedAt: first})
		if err := os.WriteFile(f.home+"/run.json", []byte("late"), 0o600); err != nil {
			t.Fatal(err)
		}
		f.poll(t)
		for _, r := range f.traces.AllTraces() {
			for _, s := range r.ResourceSpans().All() {
				for _, ss := range s.ScopeSpans().All() {
					for _, span := range ss.Spans().All() {
						if _, ok := span.Attributes().Get("gen_ai.tool.call.id"); ok {
							t.Fatal("file tool emitted at cutoff")
						}
					}
				}
			}
		}
	})
	t.Run("matching native suppresses immediately", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		key := registry.Key{Harness: "fake", RunID: "run"}
		f.core.registry.NativeTool(key, "call", "native")
		f.poll(t)
		if f.traces.SpanCount() != 1 {
			t.Fatalf("spans=%d", f.traces.SpanCount())
		}
	})
	t.Run("id and link decision", func(t *testing.T) {
		r := registry.New()
		key := registry.Key{Harness: "fake", RunID: "run"}
		id, link, linked := r.NativeTool(key, "new", "native")
		if id == "" || linked || link.SpanID != "" {
			t.Fatal("unexpected link without file emission")
		}
		if _, ok := r.ReserveFile(key, "old", time.Now(), time.Minute); !ok {
			t.Fatal("reserve")
		}
		r.CommitFile(key, "old")
		id, link, linked = r.NativeTool(key, "old", "native-id")
		if id != "native-id" || !linked || link.SpanID == "" {
			t.Fatal("emitted file span was not linked")
		}
	})
}

func TestFactorySharedCore(t *testing.T) {
	factory := NewFactory()
	cfg := createDefaultConfig()
	settings := receivertest.NewNopSettings(metadataType())
	a, err := factory.CreateTraces(context.Background(), settings, cfg, &consumertest.TracesSink{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := factory.CreateLogs(context.Background(), settings, cfg, &consumertest.LogsSink{})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("signals do not share a receiver")
	}
}
func metadataType() component.Type { return component.MustNewType("ember_sessions") }

var _ consumer.Traces = (*consumertest.TracesSink)(nil)

type registryHost struct {
	extensions map[component.ID]component.Component
}

func (h registryHost) GetExtensions() map[component.ID]component.Component { return h.extensions }

func TestReceiverLifecycleWithRegistry(t *testing.T) {
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
	receiver, err := NewFactory().CreateTraces(ctx, receivertest.NewNopSettings(component.MustNewType("ember_sessions")), cfg, &consumertest.TracesSink{})
	if err != nil {
		t.Fatal(err)
	}
	if err = receiver.Start(ctx, host); err != nil {
		t.Fatal(err)
	}
	if err = receiver.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err = receiver.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err = ext.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
