package sessions

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

type pathAdapter struct {
	run          harness.Run
	home, parsed string
}

func (a *pathAdapter) Name() string                                             { return "path-fake" }
func (a *pathAdapter) Detect(harness.Env) harness.Detection                     { return harness.Detection{} }
func (a *pathAdapter) Setup(harness.Env, harness.SetupOptions) []harness.Change { return nil }
func (a *pathAdapter) Sessions(env harness.Env) ([]harness.SessionRef, error) {
	a.home = env.Home
	return []harness.SessionRef{{RunID: a.run.SessionID, Files: []string{"run.json"}}}, nil
}
func (a *pathAdapter) Parse(fsys fs.FS, ref harness.SessionRef) (harness.Run, error) {
	a.parsed = ref.Files[0]
	if _, err := fs.Stat(fsys, ref.Files[0]); err != nil {
		return harness.Run{}, err
	}
	return a.run, nil
}
func (a *pathAdapter) Mapping() harness.Mapping { return harness.Mapping{} }

func TestSeparateHarnessAndStateHomes(t *testing.T) {
	userHome, stateHome := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(userHome, "run.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &pathAdapter{run: harness.Run{Harness: "path-fake", SessionID: "run", Status: harness.StatusCompleted, StartedAt: time.Now().Add(-time.Minute), EndedAt: time.Now()}}
	c := newCore(createDefaultConfig().(*Config), []harness.Adapter{a})
	c.registry = registry.New()
	c.userHome = userHome
	c.traces = &consumertest.TracesSink{}
	if err := c.start(stateHome); err != nil {
		t.Fatal(err)
	}
	if err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.home != userHome || a.parsed != "run.json" {
		t.Fatalf("home=%q parsed=%q", a.home, a.parsed)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "state", "sessions.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(userHome, "state", "sessions.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state written to harness home")
	}
}
func TestResolvePreservesRichSnapshot(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	f.adapter.run.Owner = "owner"
	f.poll(t)
	f.core.registry.SetSource(f.core)
	key := registry.Key{Harness: "fake", RunID: "run"}
	before, _ := f.core.registry.Get(key)
	after, err := f.core.registry.Resolve(context.Background(), key, before.LastRecord.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if after.TraceID != before.TraceID || after.RootKey == "" || after.RootSessionID == "" || after.WorkID == "" || len(after.Segments) == 0 || after.ToolSpans["call"].TraceID == "" || after.StartedBy == "" {
		t.Fatalf("run-only snapshot: %+v", after)
	}
}
func TestRescanBeyondLookback(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "persisted"}[restart], func(t *testing.T) {
			f := newFixture(t, harness.StatusCompleted)
			f.core.config.Lookback = time.Hour
			f.core.config.Rescan = 72 * time.Hour
			f.core.now = func() time.Time { return f.adapter.run.StartedAt.Add(3 * time.Hour) }
			if restart {
				f.poll(t)
				f.resetCounts()
				c := newCore(f.coreConfig(), []harness.Adapter{f.adapter})
				c.registry = registry.New()
				c.traces = f.traces
				c.logs = f.logs
				c.config.Lookback = time.Hour
				c.config.Rescan = 72 * time.Hour
				c.now = f.core.now
				if err := c.start(f.home); err != nil {
					t.Fatal(err)
				}
				f.core = c
			}
			f.poll(t)
			if f.traces.SpanCount() == 0 || f.logs.LogRecordCount() == 0 {
				t.Fatal("old rescan was limited by lookback")
			}
		})
	}
}

type traceConsumerFunc func(context.Context, ptrace.Traces) error

func (f traceConsumerFunc) ConsumeTraces(ctx context.Context, in ptrace.Traces) error {
	return f(ctx, in)
}
func (f traceConsumerFunc) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

type logConsumerFunc func(context.Context, plog.Logs) error

func (f logConsumerFunc) ConsumeLogs(ctx context.Context, in plog.Logs) error { return f(ctx, in) }
func (f logConsumerFunc) Capabilities() consumer.Capabilities                 { return consumer.Capabilities{} }

type metricConsumerFunc func(context.Context, pmetric.Metrics) error

func (f metricConsumerFunc) ConsumeMetrics(ctx context.Context, in pmetric.Metrics) error {
	return f(ctx, in)
}
func (f metricConsumerFunc) Capabilities() consumer.Capabilities { return consumer.Capabilities{} }

func TestPartialAcceptance(t *testing.T) {
	t.Run("traces", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		first := true
		f.core.traces = traceConsumerFunc(func(ctx context.Context, in ptrace.Traces) error {
			if !first {
				return errors.New("unexpected retry inside consumer")
			}
			first = false
			failed := ptrace.NewTraces()
			in.CopyTo(failed)
			accepted := ptrace.NewTraces()
			in.CopyTo(accepted)
			for _, r := range failed.ResourceSpans().All() {
				for _, s := range r.ScopeSpans().All() {
					s.Spans().RemoveIf(func(span ptrace.Span) bool {
						v, _ := span.Attributes().Get("gen_ai.tool.call.id")
						return v.Str() == ""
					})
				}
			}
			for _, r := range accepted.ResourceSpans().All() {
				for _, s := range r.ScopeSpans().All() {
					s.Spans().RemoveIf(func(span ptrace.Span) bool {
						v, _ := span.Attributes().Get("gen_ai.tool.call.id")
						return v.Str() != ""
					})
				}
			}
			if err := f.traces.ConsumeTraces(ctx, accepted); err != nil {
				return err
			}
			return consumererror.NewTraces(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want partial error")
		}
		if f.traces.SpanCount() != 1 {
			t.Fatalf("accepted=%d", f.traces.SpanCount())
		}
		f.core.traces = f.traces
		f.resetCounts()
		f.poll(t)
		if f.traces.SpanCount() != 1 {
			t.Fatalf("retry=%d", f.traces.SpanCount())
		}
		f.resetCounts()
		f.poll(t)
		if f.traces.SpanCount() != 0 {
			t.Fatal("duplicate after partial retry")
		}
	})
	t.Run("logs holes and restart", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		first := true
		f.core.logs = logConsumerFunc(func(ctx context.Context, in plog.Logs) error {
			first = false
			failed := plog.NewLogs()
			in.CopyTo(failed)
			accepted := plog.NewLogs()
			in.CopyTo(accepted)
			selectTurn := func(r plog.ResourceLogs, want bool) {
				for _, s := range r.ScopeLogs().All() {
					s.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
						v, _ := record.Attributes().Get("event.name")
						return (v.Str() == "ember.llm.turn") != want
					})
				}
			}
			for _, r := range failed.ResourceLogs().All() {
				selectTurn(r, true)
			}
			for _, r := range accepted.ResourceLogs().All() {
				selectTurn(r, false)
			}
			if err := f.logs.ConsumeLogs(ctx, accepted); err != nil {
				return err
			}
			return consumererror.NewLogs(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil || first {
			t.Fatal("partial consumer not called")
		}
		f.core.logs = f.logs
		f.resetCounts()
		f.poll(t)
		if f.logs.LogRecordCount() != 1 {
			t.Fatalf("retry logs=%d", f.logs.LogRecordCount())
		}
		f.resetCounts()
		f.core = f.newCore()
		f.poll(t)
		if f.logs.LogRecordCount() != 0 {
			t.Fatal("restart duplicated accepted log hole")
		}
	})
	t.Run("metrics resources", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		other := &fakeAdapter{run: f.adapter.run}
		other.run.SessionID = "other"
		f.core.adapters = []harness.Adapter{f.adapter, other}
		first := true
		f.core.metrics = metricConsumerFunc(func(ctx context.Context, in pmetric.Metrics) error {
			first = false
			failed := pmetric.NewMetrics()
			in.CopyTo(failed)
			accepted := pmetric.NewMetrics()
			in.CopyTo(accepted)
			failed.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool {
				id, _ := r.Resource().Attributes().Get("service.instance.id")
				return id.Str() != "other"
			})
			accepted.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool {
				id, _ := r.Resource().Attributes().Get("service.instance.id")
				return id.Str() == "other"
			})
			if err := f.metrics.ConsumeMetrics(ctx, accepted); err != nil {
				return err
			}
			return consumererror.NewMetrics(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil || first {
			t.Fatal("partial metric consumer not called")
		}
		if !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics || f.core.state.Runs[stateKey("fake", "other")].FinalMetrics {
			t.Fatal("wrong metric checkpoints")
		}
		f.core.metrics = f.metrics
		f.resetCounts()
		f.poll(t)
		if f.metrics.DataPointCount() == 0 {
			t.Fatal("failed resource not retried")
		}
		if !f.core.state.Runs[stateKey("fake", "other")].FinalMetrics {
			t.Fatal("retry not checkpointed")
		}
	})
}
func TestHeldRunHasNoFinalMetricCheckpoint(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	orphan := &fakeAdapter{run: f.adapter.run}
	orphan.run.SessionID = "orphan"
	orphan.run.Parent = &harness.ParentRef{SessionID: "missing"}
	f.core.adapters = []harness.Adapter{f.adapter, orphan}
	f.poll(t)
	if f.core.state.Runs[stateKey("fake", "orphan")].FinalMetrics {
		t.Fatal("held run marked final")
	}
	if f.metrics.DataPointCount() == 0 {
		t.Fatal("ordinary due run suppressed")
	}
}
func TestNativeSuppressionSurvivesRestart(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	key := registry.Key{Harness: "fake", RunID: "run"}
	f.core.registry.NativeTool(key, "call", "native")
	f.poll(t)
	f.resetCounts()
	f.core = f.newCore()
	f.poll(t)
	if f.traces.SpanCount() != 0 {
		t.Fatalf("file tool duplicated after restart: %d", f.traces.SpanCount())
	}
	if f.core.registry.NativeCalls(key)["call"] != "native" {
		t.Fatal("native call not restored")
	}
}
func TestHarnessQualifiedRunState(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	other := &fakeAdapter{run: f.adapter.run, name: "other-fake"}
	other.run.Harness = "other-fake"
	f.core.adapters = []harness.Adapter{f.adapter, other}
	f.poll(t)
	if len(f.core.state.Runs) != 2 || f.traces.SpanCount() != 4 {
		t.Fatalf("runs=%d spans=%d", len(f.core.state.Runs), f.traces.SpanCount())
	}
}
func TestStateValidationAndRenameErrors(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{}", `{"version":1,"runs":{"run":null}}`, `{"version":1,"runs":{"wrong":{"run":{"Harness":"fake","SessionID":"run"}}}}`} {
		if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadState(home); err == nil {
			t.Fatalf("accepted state %s", body)
		}
	}
	if err := os.Remove(filepath.Join(dir, "sessions.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sessions.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveState(home, emptyState()); err == nil {
		t.Fatal("rename error was ignored")
	}
}

var errDiscovery = errors.New("discovery failed")
var errParse = errors.New("parse failed")
var errConsume = errors.New("consume failed")

type parseErrorAdapter struct{ pathAdapter }

func (a *parseErrorAdapter) Name() string { return "parse-fake" }
func (a *parseErrorAdapter) Parse(fs.FS, harness.SessionRef) (harness.Run, error) {
	return harness.Run{}, errParse
}
func TestPollJoinsErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "run.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute)
	good := &pathAdapter{run: harness.Run{Harness: "path-fake", SessionID: "good", Status: harness.StatusCompleted, StartedAt: start, EndedAt: start}}
	bad := &parseErrorAdapter{pathAdapter{run: harness.Run{Harness: "parse-fake", SessionID: "bad"}}}
	c := newCore(createDefaultConfig().(*Config), []harness.Adapter{good, bad, &errorAdapter{}})
	c.registry = registry.New()
	c.userHome = home
	c.traces = consumertest.NewErr(errConsume)
	if err := c.start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	err := c.Poll(context.Background())
	for _, want := range []error{errDiscovery, errParse, errConsume} {
		if !errors.Is(err, want) {
			t.Fatalf("%v does not include %v", err, want)
		}
	}
}
func TestTelemetryLoggerUsed(t *testing.T) {
	observed, logs := observer.New(zap.DebugLevel)
	home := t.TempDir()
	cfg := createDefaultConfig().(*Config)
	cfg.Home = home
	r := newSessionReceiver(cfg, zap.New(observed))
	r.core.adapters = []harness.Adapter{&errorAdapter{}}
	r.core.registry = registry.New()
	r.core.userHome = home
	if err := r.core.start(home); err != nil {
		t.Fatal(err)
	}
	r.reportPoll(r.core.Poll(context.Background()))
	if logs.FilterMessage("session poll failed").Len() != 1 {
		t.Fatal("poll error not sent to telemetry logger")
	}
}

type errorAdapter struct{}

func (*errorAdapter) Name() string                                             { return "error-fake" }
func (*errorAdapter) Detect(harness.Env) harness.Detection                     { return harness.Detection{} }
func (*errorAdapter) Setup(harness.Env, harness.SetupOptions) []harness.Change { return nil }
func (*errorAdapter) Sessions(harness.Env) ([]harness.SessionRef, error) {
	return nil, errDiscovery
}
func (*errorAdapter) Parse(fs.FS, harness.SessionRef) (harness.Run, error) { return harness.Run{}, nil }
func (*errorAdapter) Mapping() harness.Mapping                             { return harness.Mapping{} }
