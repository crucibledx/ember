package sessions

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

type signalTraceConsumerFunc func(context.Context, ptrace.Traces) error

func (f signalTraceConsumerFunc) ConsumeTraces(ctx context.Context, in ptrace.Traces) error {
	return f(ctx, in)
}
func (signalTraceConsumerFunc) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type signalLogConsumerFunc func(context.Context, plog.Logs) error

func (f signalLogConsumerFunc) ConsumeLogs(ctx context.Context, in plog.Logs) error {
	return f(ctx, in)
}
func (signalLogConsumerFunc) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type signalMetricConsumerFunc func(context.Context, pmetric.Metrics) error

func (f signalMetricConsumerFunc) ConsumeMetrics(ctx context.Context, in pmetric.Metrics) error {
	return f(ctx, in)
}
func (signalMetricConsumerFunc) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func TestSignalCheckpointsSurviveMutatingConsumers(t *testing.T) {
	t.Run("traces", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.core.traces = signalTraceConsumerFunc(func(_ context.Context, in ptrace.Traces) error {
			failed := ptrace.NewTraces()
			in.CopyTo(failed)
			for _, resource := range failed.ResourceSpans().All() {
				for _, scope := range resource.ScopeSpans().All() {
					scope.Spans().RemoveIf(func(span ptrace.Span) bool {
						_, tool := span.Attributes().Get("gen_ai.tool.call.id")
						return tool
					})
				}
			}
			moved := ptrace.NewTraces()
			in.ResourceSpans().MoveAndAppendTo(moved.ResourceSpans())
			return consumererror.NewTraces(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want partial error")
		}
		state := f.core.state.Runs[stateKey("fake", "run")]
		if len(state.Tools) != 1 || state.Tools[0] != "call" {
			t.Fatalf("accepted tool was not checkpointed: %v", state.Tools)
		}
		_, _, linked := f.core.registry.NativeTool(registry.Key{Harness: "fake", RunID: "run"}, "call", "native")
		if !linked {
			t.Fatal("accepted native reservation was not committed")
		}
		f.core.traces = f.traces
		f.resetCounts()
		f.poll(t)
		if got := f.traces.SpanCount(); got != 1 {
			t.Fatalf("rejected spans retried=%d", got)
		}
	})

	t.Run("metrics", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.core.metrics = signalMetricConsumerFunc(func(_ context.Context, in pmetric.Metrics) error {
			moved := pmetric.NewMetrics()
			in.ResourceMetrics().MoveAndAppendTo(moved.ResourceMetrics())
			return nil
		})
		f.poll(t)
		if !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
			t.Fatal("accepted final metrics were not checkpointed")
		}
	})
}

func TestLogCheckpointUsesStableIdentities(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	at := f.adapter.run.StartedAt.Add(1)
	f.adapter.run.TurnTimes = []time.Time{at, at}
	first := true
	f.core.logs = signalLogConsumerFunc(func(_ context.Context, in plog.Logs) error {
		if !first {
			t.Fatal("unexpected retry in mutating consumer")
		}
		first = false
		failed := plog.NewLogs()
		in.CopyTo(failed)
		for _, resource := range failed.ResourceLogs().All() {
			for _, scope := range resource.ScopeLogs().All() {
				scope.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
					turn, ok := record.Attributes().Get("ember.llm.turn")
					return !ok || turn.Int() != 2
				})
			}
		}
		moved := plog.NewLogs()
		in.ResourceLogs().MoveAndAppendTo(moved.ResourceLogs())
		return consumererror.NewLogs(errors.New("partial"), failed)
	})
	if err := f.core.Poll(context.Background()); err == nil {
		t.Fatal("want partial error")
	}
	f.core.logs = f.logs
	f.resetCounts()
	f.poll(t)
	assertLogTurns(t, f.logs.AllLogs(), 2)
	f.resetCounts()
	f.adapter.run.TurnTimes = append(f.adapter.run.TurnTimes, at)
	if err := os.WriteFile(f.home+"/run.json", []byte("third turn"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	assertLogTurns(t, f.logs.AllLogs(), 3)
	f.resetCounts()
	f.poll(t)
	if got := f.logs.LogRecordCount(); got != 0 {
		t.Fatalf("logs duplicated after incremental addition: %d", got)
	}
}

func TestEqualTimeInsertionDoesNotShiftLogCheckpoints(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	at := f.adapter.run.StartedAt.Add(time.Second)
	f.adapter.run.TurnTimes = []time.Time{at}
	f.adapter.run.ToolCalls[0].StartedAt = at.Add(-time.Second)
	f.adapter.run.ToolCalls[0].EndedAt = at
	f.poll(t)
	f.resetCounts()
	f.adapter.run.TurnTimes = append(f.adapter.run.TurnTimes, at)
	if err := os.WriteFile(f.home+"/run.json", []byte("second equal-time turn"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if got := f.logs.LogRecordCount(); got != 1 {
		t.Fatalf("equal-time insertion emitted %d logs, want only the new turn", got)
	}
	assertLogTurns(t, f.logs.AllLogs(), 2)
	f.resetCounts()
	f.poll(t)
	if got := f.logs.LogRecordCount(); got != 0 {
		t.Fatalf("equal-time records duplicated: %d", got)
	}
}

func TestIdleTerminalTransitionInvalidatesFinalMetrics(t *testing.T) {
	f := newFixture(t, harness.StatusRunning)
	f.poll(t)
	state := f.core.state.Runs[stateKey("fake", "run")]
	if !state.FinalMetrics {
		t.Fatal("idle run did not emit final metrics")
	}
	f.resetCounts()
	f.adapter.run.Status = harness.StatusCompleted
	if err := os.WriteFile(f.home+"/run.json", []byte("terminal without new activity"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if !state.FinalMetrics || f.metrics.DataPointCount() == 0 {
		t.Fatal("idle-to-terminal transition suppressed the closing metrics")
	}
}

func TestIdleResumeCloseInvalidatesFinalMetrics(t *testing.T) {
	f := newFixture(t, harness.StatusRunning)
	f.poll(t)
	state := f.core.state.Runs[stateKey("fake", "run")]
	if !state.FinalMetrics {
		t.Fatal("idle run did not emit final metrics")
	}
	resumedAt := f.core.now()
	f.adapter.run.TurnTimes = append(f.adapter.run.TurnTimes, resumedAt)
	if err := os.WriteFile(f.home+"/run.json", []byte("resumed"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.resetCounts()
	f.poll(t)
	if state.FinalMetrics || f.metrics.DataPointCount() == 0 {
		t.Fatal("resumed activity did not invalidate and emit metrics")
	}
	f.adapter.run.Status = harness.StatusCompleted
	f.adapter.run.EndedAt = resumedAt.Add(time.Minute)
	if err := os.WriteFile(f.home+"/run.json", []byte("closed"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if !state.FinalMetrics {
		t.Fatal("resumed run did not checkpoint its new final metrics")
	}
	f.resetCounts()
	f.poll(t)
	if got := f.metrics.DataPointCount(); got != 0 {
		t.Fatalf("resumed run repeated final metrics: %d", got)
	}
}

func TestStartupMetricRejectionsRemainPendingPerRun(t *testing.T) {
	t.Run("full rejection", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.poll(t)
		f.core = f.newCore()
		f.core.metrics = signalMetricConsumerFunc(func(_ context.Context, in pmetric.Metrics) error {
			moved := pmetric.NewMetrics()
			in.ResourceMetrics().MoveAndAppendTo(moved.ResourceMetrics())
			return errors.New("retry")
		})
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want consumer error")
		}
		if f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
			t.Fatal("rejected startup snapshot was checkpointed")
		}
		f.core.metrics = f.metrics
		f.resetCounts()
		f.poll(t)
		if f.metrics.DataPointCount() == 0 || !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
			t.Fatal("rejected startup snapshot was not retried and checkpointed")
		}
		f.resetCounts()
		f.poll(t)
		if got := f.metrics.DataPointCount(); got != 0 {
			t.Fatalf("accepted startup snapshot duplicated: %d", got)
		}
	})

	t.Run("partial rejection", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		other := &fakeAdapter{run: f.adapter.run}
		other.run.SessionID = "other"
		f.core.adapters = []harness.Adapter{f.adapter, other}
		f.poll(t)
		f.core = f.newCore()
		f.core.metrics = signalMetricConsumerFunc(func(_ context.Context, in pmetric.Metrics) error {
			failed := pmetric.NewMetrics()
			in.CopyTo(failed)
			failed.ResourceMetrics().RemoveIf(func(resource pmetric.ResourceMetrics) bool {
				id, _ := resource.Resource().Attributes().Get("service.instance.id")
				return id.Str() != "other"
			})
			moved := pmetric.NewMetrics()
			in.ResourceMetrics().MoveAndAppendTo(moved.ResourceMetrics())
			return consumererror.NewMetrics(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want partial error")
		}
		if !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics || f.core.state.Runs[stateKey("fake", "other")].FinalMetrics {
			t.Fatal("partial startup checkpoints were not per run")
		}
		f.core.metrics = f.metrics
		f.resetCounts()
		f.poll(t)
		for _, metrics := range f.metrics.AllMetrics() {
			for _, resource := range metrics.ResourceMetrics().All() {
				id, _ := resource.Resource().Attributes().Get("service.instance.id")
				if id.Str() != "other" {
					t.Fatalf("accepted run %q duplicated on retry", id.Str())
				}
			}
		}
		if f.metrics.DataPointCount() == 0 || !f.core.state.Runs[stateKey("fake", "other")].FinalMetrics {
			t.Fatal("rejected run was not retried and checkpointed")
		}
	})
}

func assertLogTurns(t *testing.T, logs []plog.Logs, want ...int64) {
	t.Helper()
	var got []int64
	for _, batch := range logs {
		for _, resource := range batch.ResourceLogs().All() {
			for _, scope := range resource.ScopeLogs().All() {
				for _, record := range scope.LogRecords().All() {
					if turn, ok := record.Attributes().Get("ember.llm.turn"); ok {
						got = append(got, turn.Int())
					}
				}
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("log turns=%v, want %v", got, want)
	}
}
