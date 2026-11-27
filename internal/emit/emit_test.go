package emit_test

import (
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/golden"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/pmetrictest"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

var start = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

func eq(t *testing.T, got, want any) {
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func run(id string) harness.Run {
	return harness.Run{Harness: "test-harness", SessionID: id, Kind: harness.KindAgent, AgentType: "worker", WorkstreamID: "w",
		Status: harness.StatusCompleted, StartedAt: start, EndedAt: start.Add(2 * time.Minute), Model: "model-a", LLMTurns: 2,
		TurnTimes: []time.Time{start, start.Add(time.Minute)}, Usage: map[string]harness.Usage{
			"model-a": {Input: 10, Output: 5, CacheRead: 3, CacheWrite: 2, Credits: 1.5}, "model-b": {Input: 4, Output: 2}},
		ToolCalls: []harness.ToolCall{{ID: "call", Name: "mcp__server__read", StartedAt: start.Add(time.Minute), EndedAt: start.Add(2 * time.Minute), IsError: true}}}
}
func forest(o tree.Options, runs ...harness.Run) *tree.Forest { return tree.BuildForest(runs, o) }
func options() tree.Options {
	return tree.Options{Now: start.Add(3 * time.Minute), Idle: 30 * time.Second}
}
func metric(metrics pmetric.Metrics, session, name string) pmetric.Metric {
	for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
		resource := metrics.ResourceMetrics().At(i)
		id, _ := resource.Resource().Attributes().Get("service.instance.id")
		if id.AsString() != session {
			continue
		}
		items := resource.ScopeMetrics().At(0).Metrics()
		for j := 0; j < items.Len(); j++ {
			if items.At(j).Name() == name {
				return items.At(j)
			}
		}
	}
	return pmetric.NewMetric()
}
func point(m pmetric.Metric, model, kind string) pmetric.NumberDataPoint {
	points := m.Sum().DataPoints()
	for i := 0; i < points.Len(); i++ {
		attrs := points.At(i).Attributes()
		mv, mok := attrs.Get("gen_ai.request.model")
		tv, tok := attrs.Get("gen_ai.token.type")
		if (!mok || mv.AsString() == model) && (!tok || tv.AsString() == kind) {
			return points.At(i)
		}
	}
	return pmetric.NewNumberDataPoint()
}
func lineageOnly(metrics pmetric.Metrics) pmetric.Metrics {
	out := pmetric.NewMetrics()
	source := metrics.ResourceMetrics().At(0)
	target := out.ResourceMetrics().AppendEmpty()
	source.Resource().CopyTo(target.Resource())
	ss, ts := source.ScopeMetrics().At(0), target.ScopeMetrics().AppendEmpty()
	ss.Scope().CopyTo(ts.Scope())
	metric(metrics, "one", "ember.lineage").CopyTo(ts.Metrics().AppendEmpty())
	return out
}
func TestLogs(t *testing.T) {
	first, second := run("one"), run("two")
	second.TurnTimes, second.ToolCalls, second.Usage = nil, nil, nil
	logs := emit.Logs(forest(options(), first, second), emit.Config{})
	traces := emit.Traces(forest(options(), first, second), emit.Config{})
	exported := map[string]bool{}
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		spans := traces.ResourceSpans().At(i).ScopeSpans().At(0).Spans()
		for j := 0; j < spans.Len(); j++ {
			exported[spans.At(j).TraceID().String()+"/"+spans.At(j).SpanID().String()] = true
		}
	}
	counts := map[string]int{}
	var failedBody, failedSeverity string
	eq(t, logs.ResourceLogs().Len(), 2)
	for i := 0; i < logs.ResourceLogs().Len(); i++ {
		records := logs.ResourceLogs().At(i).ScopeLogs().At(0).LogRecords()
		var previous pcommon.Timestamp
		for j := 0; j < records.Len(); j++ {
			record := records.At(j)
			if j > 0 && record.Timestamp() < previous {
				t.Fatal("records out of order")
			}
			previous = record.Timestamp()
			eq(t, exported[record.TraceID().String()+"/"+record.SpanID().String()], true)
			event, _ := record.Attributes().Get("event.name")
			counts[event.AsString()]++
			if call, ok := record.Attributes().Get("gen_ai.tool.call.id"); ok && call.AsString() == "call" {
				failedBody, failedSeverity = record.Body().AsString(), record.SeverityText()
			}
		}
	}
	eq(t, counts, map[string]int{emit.AgentStarted: 2, emit.AgentEnded: 2, emit.LLMTurn: 2, emit.ToolCall: 1})
	eq(t, []string{failedSeverity, failedBody}, []string{"ERROR", "tool mcp__server__read error 60000ms"})
	t.Run("running and interrupted eligibility", func(t *testing.T) {
		active := run("active")
		active.Status, active.EndedAt = harness.StatusRunning, time.Time{}
		active.TurnTimes = []time.Time{start}
		active.ToolCalls[0].StartedAt, active.ToolCalls[0].EndedAt = start, time.Time{}
		active.ToolCalls = append(active.ToolCalls, harness.ToolCall{ID: "done", Name: "done", StartedAt: start, EndedAt: start})
		o := tree.Options{Now: start, Idle: 30 * time.Second}
		f := forest(o, active)
		eq(t, emit.Traces(f, emit.Config{}).SpanCount(), 1)
		records := emit.Logs(f, emit.Config{}).ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
		events := map[string]int{}
		for i := 0; i < records.Len(); i++ {
			event, _ := records.At(i).Attributes().Get("event.name")
			events[event.AsString()]++
		}
		eq(t, events, map[string]int{emit.AgentStarted: 1, emit.LLMTurn: 1, emit.ToolCall: 1})
		active.Status, active.EndedAt = harness.StatusCompleted, start.Add(3*time.Minute)
		f = forest(options(), active)
		eq(t, emit.Traces(f, emit.Config{}).SpanCount(), 4)
		records = emit.Logs(f, emit.Config{}).ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
		eq(t, records.Len(), 5)
	})
}
func TestMetrics(t *testing.T) {
	config := emit.Config{MCP: map[string]*regexp.Regexp{"test-harness": regexp.MustCompile(`^mcp__(.+?)__(.+)$`)}}
	parent, child := run("parent"), run("one")
	child.Parent = &harness.ParentRef{SessionID: "parent"}
	child.Owner = "owner"
	o := options()
	o.Attribution = []tree.AttributionRecord{{At: start, Human: "active", Source: "s", SessionID: "one", Role: "active"}}
	metrics := emit.Metrics(forest(o, parent, child), config, start.Add(3*time.Minute))
	lineage := metric(metrics, "one", "ember.lineage").Gauge().DataPoints().At(0)
	eq(t, lineage.Attributes().AsRaw(), map[string]any{
		"session.id": "one", "ember.parent.session.id": "parent", "ember.root.session.id": "parent", "ember.agent.kind": "agent", "gen_ai.agent.name": "worker",
		"ember.harness": "test-harness", "ember.workstream.id": "w", "ember.work.id": "w", "ember.human.started_by": "test-harness:owner",
		"ember.human.active": "s:active,test-harness:owner", "ember.agent.status": "completed", "ember.agent.children": int64(0)})
	tokens := metric(metrics, "one", "ember.usage.tokens")
	eq(t, tokens.Sum().DataPoints().Len(), 8)
	for _, tc := range []struct {
		model, kind string
		want        int64
	}{{"model-a", "input", 10}, {"model-a", "output", 5}, {"model-a", "cache_read", 3}, {"model-a", "cache_write", 2}, {"model-b", "input", 4}, {"model-b", "output", 2}} {
		p := point(tokens, tc.model, tc.kind)
		eq(t, p.IntValue(), tc.want)
		eq(t, p.StartTimestamp(), pcommon.Timestamp(start.UnixNano()))
		eq(t, p.Attributes().AsRaw(), map[string]any{"session.id": "one", "ember.harness": "test-harness", "gen_ai.request.model": tc.model, "gen_ai.token.type": tc.kind, "ember.workstream.id": "w", "ember.work.id": "w"})
	}
	credit := point(metric(metrics, "one", "ember.usage.credits"), "model-a", "")
	eq(t, credit.DoubleValue(), 1.5)
	eq(t, credit.StartTimestamp(), pcommon.Timestamp(start.UnixNano()))
	eq(t, credit.Attributes().AsRaw(), map[string]any{"session.id": "one", "ember.harness": "test-harness", "gen_ai.request.model": "model-a", "ember.workstream.id": "w", "ember.work.id": "w"})
	tool := metric(metrics, "one", "ember.tool.calls").Sum().DataPoints().At(0)
	eq(t, tool.StartTimestamp(), pcommon.Timestamp(start.UnixNano()))
	eq(t, tool.Attributes().AsRaw(), map[string]any{"session.id": "one", "ember.harness": "test-harness", "gen_ai.tool.name": "mcp__server__read", "ember.mcp.server": "server", "ember.mcp.tool": "read", "error.type": "tool_error", "ember.workstream.id": "w", "ember.work.id": "w"})
	namer := otlptranslator.NewMetricNamer("", otlptranslator.UnderscoreEscapingWithSuffixes)
	for name, want := range map[string]string{"ember.lineage": "ember_lineage", "ember.usage.tokens": "ember_usage_tokens_total", "ember.usage.credits": "ember_usage_credits_total", "ember.tool.calls": "ember_tool_calls_total"} {
		m := metric(metrics, "one", name)
		var kind otlptranslator.MetricType = otlptranslator.MetricTypeMonotonicCounter
		if m.Type() == pmetric.MetricTypeGauge {
			kind = otlptranslator.MetricTypeGauge
		}
		got, err := namer.Build(otlptranslator.Metric{Name: m.Name(), Unit: m.Unit(), Type: kind})
		eq(t, err, nil)
		eq(t, got, want)
	}
	t.Run("state clamps resets retains unknown and stabilizes tools", func(t *testing.T) {
		state := &emit.MetricsState{}
		before := run("state")
		before.Usage = map[string]harness.Usage{"unknown": {Input: 10, Credits: 2}}
		eq(t, point(metric(state.Metrics(forest(options(), before), config, start), "state", "ember.usage.tokens"), "unknown", "input").IntValue(), int64(10))
		after := before
		after.Model = "new-model"
		after.Usage = map[string]harness.Usage{"unknown": {Input: 5, Credits: 1}}
		reset := state.Metrics(forest(options(), after), config, start.Add(time.Minute))
		eq(t, point(metric(reset, "state", "ember.usage.tokens"), "unknown", "input").IntValue(), int64(10))
		eq(t, point(metric(reset, "state", "ember.usage.credits"), "unknown", "").DoubleValue(), float64(2))
		eq(t, emit.MetricDue(after, start.Add(1000*time.Hour), time.Minute, time.Hour, false, false), false)
		after.Usage = map[string]harness.Usage{"unknown": {Input: 20, Credits: 3}}
		later := state.Metrics(forest(options(), after), config, start.Add(4*time.Minute))
		eq(t, point(metric(later, "state", "ember.usage.tokens"), "unknown", "input").IntValue(), int64(20))
		eq(t, point(metric(later, "state", "ember.usage.credits"), "unknown", "").DoubleValue(), float64(3))
		active := before
		active.Status, active.EndedAt = harness.StatusRunning, time.Time{}
		active.ToolCalls[0].EndedAt = time.Time{}
		eq(t, metric(state.Metrics(forest(options(), active), config, start), "state", "ember.tool.calls").Sum().DataPoints().Len(), 0)
		active.Status, active.EndedAt, active.ToolCalls[0].EndedAt = harness.StatusCompleted, start.Add(2*time.Minute), start.Add(2*time.Minute)
		calls := metric(state.Metrics(forest(options(), active), config, start), "state", "ember.tool.calls").Sum().DataPoints()
		eq(t, calls.Len(), 1)
		eq(t, calls.At(0).IntValue(), int64(1))
	})
	t.Run("schedule and golden", func(t *testing.T) {
		active := run("one")
		active.Status, active.EndedAt = harness.StatusRunning, time.Time{}
		for _, tc := range []struct {
			name                 string
			run                  harness.Run
			now                  time.Time
			startup, final, want bool
		}{
			{"active", active, start.Add(time.Minute), false, false, true}, {"idle", active, start.Add(time.Hour), false, false, false},
			{"close", run("one"), start.Add(time.Hour), false, false, true}, {"closed", run("one"), start.Add(time.Hour), false, true, false},
			{"old unsent", run("one"), start.Add(1000 * time.Hour), false, false, false}, {"old startup", run("one"), start.Add(1000 * time.Hour), true, false, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				eq(t, emit.MetricDue(tc.run, tc.now, 30*time.Minute, 168*time.Hour, tc.startup, tc.final), tc.want)
			})
		}
		expected, err := golden.ReadMetrics("testdata/lineage.json")
		eq(t, err, nil)
		actual := lineageOnly(emit.Metrics(forest(options(), run("one")), emit.Config{}, start.Add(3*time.Minute)))
		eq(t, pmetrictest.CompareMetrics(expected, actual), nil)
	})
}
