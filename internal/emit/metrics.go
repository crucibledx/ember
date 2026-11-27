package emit

import (
	"maps"
	"slices"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

var tokenTypes = []struct {
	name  string
	value func(harness.Usage) int64
}{
	{"input", func(u harness.Usage) int64 { return u.Input }},
	{"output", func(u harness.Usage) int64 { return u.Output }},
	{"cache_read", func(u harness.Usage) int64 { return u.CacheRead }},
	{"cache_write", func(u harness.Usage) int64 { return u.CacheWrite }},
}

func Metrics(forest *tree.Forest, config Config, now time.Time) pmetric.Metrics {
	out := pmetric.NewMetrics()
	for _, node := range forest.Ordered() {
		if node.Held {
			continue
		}
		resource := out.ResourceMetrics().AppendEmpty()
		resourceFor(resource.Resource(), node, config)
		scope := resource.ScopeMetrics().AppendEmpty()
		scope.Scope().SetName("ember")
		scope.Scope().SetVersion(config.Resource.Version)
		appendLineage(scope.Metrics().AppendEmpty(), node, now)
		appendUsage(scope.Metrics(), node, now)
		appendToolCalls(scope.Metrics().AppendEmpty(), node, config, now)
	}
	return out
}
func appendLineage(metric pmetric.Metric, node *tree.Node, now time.Time) {
	metric.SetName("ember.lineage")
	gauge := metric.SetEmptyGauge()
	point := gauge.DataPoints().AppendEmpty()
	point.SetStartTimestamp(timestamp(node.Run.StartedAt))
	point.SetTimestamp(timestamp(now))
	point.SetIntValue(1)
	attrs := point.Attributes()
	putStrings(attrs, "session.id", node.Run.SessionID, "ember.root.session.id", node.RootSessionID,
		"ember.agent.kind", string(node.Run.Kind), "gen_ai.agent.name", agentName(node.Run),
		"ember.harness", node.Run.Harness, "ember.workstream.id", node.WorkstreamID, "ember.work.id", node.WorkID,
		"ember.human.started_by", node.StartedBy, "ember.human.active", tree.ActiveHumanIDs(node.Active),
		"ember.agent.status", string(node.Run.Status))
	if node.Run.Parent != nil {
		putString(attrs, "ember.parent.session.id", node.Run.Parent.SessionID)
	}
	attrs.PutInt("ember.agent.children", int64(len(node.Children)))
}
func appendUsage(metrics pmetric.MetricSlice, node *tree.Node, now time.Time) {
	tokens := newSum(metrics.AppendEmpty(), "ember.usage.tokens", "{token}")
	credits := newSum(metrics.AppendEmpty(), "ember.usage.credits", "{credit}")
	for _, model := range slices.Sorted(maps.Keys(node.Run.Usage)) {
		usage := node.Run.Usage[model]
		for _, tokenType := range tokenTypes {
			point := tokens.DataPoints().AppendEmpty()
			setPointTimes(point, node.Run.StartedAt, now)
			point.SetIntValue(tokenType.value(usage))
			usageAttrs(point.Attributes(), node, model)
			point.Attributes().PutStr("gen_ai.token.type", tokenType.name)
		}
		if usage.Credits != 0 {
			point := credits.DataPoints().AppendEmpty()
			setPointTimes(point, node.Run.StartedAt, now)
			point.SetDoubleValue(usage.Credits)
			usageAttrs(point.Attributes(), node, model)
		}
	}
}
func newSum(metric pmetric.Metric, name, unit string) pmetric.Sum {
	metric.SetName(name)
	metric.SetUnit(unit)
	sum := metric.SetEmptySum()
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	sum.SetIsMonotonic(true)
	return sum
}
func setPointTimes(point pmetric.NumberDataPoint, start, now time.Time) {
	point.SetStartTimestamp(timestamp(start))
	point.SetTimestamp(timestamp(now))
}
func usageAttrs(attrs pcommon.Map, node *tree.Node, model string) {
	putStrings(attrs, "session.id", node.Run.SessionID, "ember.harness", node.Run.Harness, "gen_ai.request.model", model, "ember.workstream.id", node.WorkstreamID, "ember.work.id", node.WorkID)
}
func appendToolCalls(metric pmetric.Metric, node *tree.Node, config Config, now time.Time) {
	sum := newSum(metric, "ember.tool.calls", "{call}")
	groups := map[string]int64{}
	attrsByGroup := map[string]pcommon.Map{}
	for _, call := range node.Run.ToolCalls {
		if !toolEligible(node.Run, call) {
			continue
		}
		attrs := pcommon.NewMap()
		server, tool := mcpTool(config, node, call.Name)
		putStrings(attrs, "session.id", node.Run.SessionID, "ember.harness", node.Run.Harness,
			"gen_ai.tool.name", call.Name, "ember.mcp.server", server, "ember.mcp.tool", tool,
			"ember.workstream.id", node.WorkstreamID, "ember.work.id", node.WorkID)
		errorType := ""
		if call.IsError {
			errorType = "tool_error"
			attrs.PutStr("error.type", errorType)
		}
		key := call.Name + "\x00" + server + "\x00" + tool + "\x00" + errorType
		groups[key]++
		attrsByGroup[key] = attrs
	}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		point := sum.DataPoints().AppendEmpty()
		setPointTimes(point, node.Run.StartedAt, now)
		point.SetIntValue(groups[key])
		attrsByGroup[key].CopyTo(point.Attributes())
	}
}
func MetricDue(run harness.Run, now time.Time, idle, lookback time.Duration, startup, finalSent bool) bool {
	end := run.EndedAt
	if end.IsZero() {
		segments := tree.Segments(run, 0, time.Time{})
		end = segments[len(segments)-1].End
	}
	if run.Status == harness.StatusRunning {
		return !now.After(end.Add(idle)) || startup && !now.After(end.Add(lookback))
	}
	if now.After(end.Add(lookback)) {
		return false
	}
	return !finalSent || startup
}
