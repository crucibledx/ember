package sessions

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/harness"
)

func stripTraceCheckpointAttributes(traces ptrace.Traces) {
	for _, resource := range traces.ResourceSpans().All() {
		resource.Resource().Attributes().Clear()
		for _, scope := range resource.ScopeSpans().All() {
			for _, span := range scope.Spans().All() {
				span.Attributes().Clear()
			}
		}
	}
}

func stripLogCheckpointAttributes(logs plog.Logs) {
	for _, resource := range logs.ResourceLogs().All() {
		resource.Resource().Attributes().Clear()
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				record.Attributes().Clear()
			}
		}
	}
}

func stripMetricCheckpointAttributes(metrics pmetric.Metrics) {
	for _, resource := range metrics.ResourceMetrics().All() {
		resource.Resource().Attributes().Clear()
		for _, scope := range resource.ScopeMetrics().All() {
			for _, metric := range scope.Metrics().All() {
				var points pmetric.NumberDataPointSlice
				switch metric.Type() {
				case pmetric.MetricTypeGauge:
					points = metric.Gauge().DataPoints()
				case pmetric.MetricTypeSum:
					points = metric.Sum().DataPoints()
				}
				for _, point := range points.All() {
					point.Attributes().Clear()
				}
			}
		}
	}
}

func TestPrivacyMutationKeepsCheckpointIdentities(t *testing.T) {
	t.Run("traces", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.core.traces = signalTraceConsumerFunc(func(_ context.Context, in ptrace.Traces) error {
			failed := ptrace.NewTraces()
			in.CopyTo(failed)
			for _, resource := range failed.ResourceSpans().All() {
				for _, scope := range resource.ScopeSpans().All() {
					scope.Spans().RemoveIf(func(span ptrace.Span) bool {
						_, tool := span.Attributes().Get("gen_ai.tool.call.id")
						return !tool
					})
				}
			}
			stripTraceCheckpointAttributes(in)
			stripTraceCheckpointAttributes(failed)
			return consumererror.NewTraces(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want partial error")
		}
		state := f.core.state.Runs[stateKey("fake", "run")]
		if slices.Contains(state.Tools, "call") {
			t.Fatal("privacy-stripped rejected tool was checkpointed")
		}
		f.core.traces = f.traces
		f.resetCounts()
		f.poll(t)
		if got := f.traces.SpanCount(); got != 1 {
			t.Fatalf("rejected privacy-stripped tool retries=%d, want 1", got)
		}
	})

	t.Run("logs", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.adapter.run.TurnTimes = []time.Time{
			f.adapter.run.StartedAt.Add(time.Second),
			f.adapter.run.StartedAt.Add(2 * time.Second),
		}
		f.core.logs = signalLogConsumerFunc(func(_ context.Context, in plog.Logs) error {
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
			stripLogCheckpointAttributes(in)
			stripLogCheckpointAttributes(failed)
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
		f.poll(t)
		if got := f.logs.LogRecordCount(); got != 0 {
			t.Fatalf("privacy-stripped logs duplicated after retry: %d", got)
		}
	})

	t.Run("metrics", func(t *testing.T) {
		f := newFixture(t, harness.StatusCompleted)
		f.core.metrics = signalMetricConsumerFunc(func(_ context.Context, in pmetric.Metrics) error {
			failed := pmetric.NewMetrics()
			in.CopyTo(failed)
			stripMetricCheckpointAttributes(in)
			stripMetricCheckpointAttributes(failed)
			return consumererror.NewMetrics(errors.New("partial"), failed)
		})
		if err := f.core.Poll(context.Background()); err == nil {
			t.Fatal("want partial error")
		}
		if f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
			t.Fatal("privacy-stripped rejected metrics were checkpointed")
		}
		f.core.metrics = f.metrics
		f.resetCounts()
		f.poll(t)
		if f.metrics.DataPointCount() == 0 || !f.core.state.Runs[stateKey("fake", "run")].FinalMetrics {
			t.Fatal("privacy-stripped rejected metrics were not retried")
		}
		f.resetCounts()
		f.poll(t)
		if got := f.metrics.DataPointCount(); got != 0 {
			t.Fatalf("privacy-stripped metrics duplicated after retry: %d", got)
		}
	})
}
