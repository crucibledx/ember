package sessions

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/tree"
)

func metricResourceKey(resource pmetric.ResourceMetrics) string {
	runID, _ := resource.Resource().Attributes().Get("service.instance.id")
	for _, scope := range resource.ScopeMetrics().All() {
		for _, metric := range scope.Metrics().All() {
			points := pmetric.NewNumberDataPointSlice()
			switch metric.Type() {
			case pmetric.MetricTypeGauge:
				points = metric.Gauge().DataPoints()
			case pmetric.MetricTypeSum:
				points = metric.Sum().DataPoints()
			}
			if points.Len() > 0 {
				harnessName, _ := points.At(0).Attributes().Get("ember.harness")
				return stateKey(harnessName.Str(), runID.Str())
			}
		}
	}
	return ""
}
func (c *core) emitMetrics(ctx context.Context, forest *tree.Forest, now time.Time) error {
	due := map[string]bool{}
	for _, node := range forest.Ordered() {
		if node.Held {
			continue
		}
		id := stateKey(node.Run.Harness, node.Run.SessionID)
		state := c.state.Runs[id]
		due[id] = emit.MetricDue(node.Run, now, c.config.Idle, c.lookback(), c.startup, state.FinalMetrics)
	}
	out := c.metricState.Metrics(forest, emit.Config{}, now)
	out.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool { return !due[metricResourceKey(r)] })
	if out.MetricCount() == 0 {
		return nil
	}
	err := c.metrics.ConsumeMetrics(ctx, out)
	failed := map[string]bool{}
	if err != nil {
		var partial consumererror.Metrics
		if errors.As(err, &partial) {
			for _, resource := range partial.Data().ResourceMetrics().All() {
				failed[metricResourceKey(resource)] = true
			}
		} else {
			for id := range due {
				failed[id] = true
			}
		}
	}
	for _, resource := range out.ResourceMetrics().All() {
		id := metricResourceKey(resource)
		if !due[id] || failed[id] {
			continue
		}
		state := c.state.Runs[id]
		if tree.Terminal(state.Run.Status) || tree.RunClosed(state.Run, now, c.config.Idle) {
			state.FinalMetrics = true
		}
	}
	c.startup = false
	return err
}
