package sessions

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/tree"
)

func (c *core) emitMetrics(ctx context.Context, forest *tree.Forest, now time.Time) error {
	due := map[string]bool{}
	for _, node := range forest.Ordered() {
		state := c.state.Runs[node.Run.SessionID]
		due[node.Run.SessionID] = emit.MetricDue(node.Run, now, c.config.Idle, c.config.Lookback, c.startup, state.FinalMetrics)
	}
	out := c.metricState.Metrics(forest, emit.Config{}, now)
	out.ResourceMetrics().RemoveIf(func(r pmetric.ResourceMetrics) bool {
		id, _ := r.Resource().Attributes().Get("service.instance.id")
		return !due[id.Str()]
	})
	if out.MetricCount() == 0 {
		return nil
	}
	if c.metrics == nil {
		return nil
	}
	if err := c.metrics.ConsumeMetrics(ctx, out); err != nil {
		return err
	}
	for id := range due {
		if due[id] {
			state := c.state.Runs[id]
			if tree.Terminal(state.Run.Status) || tree.RunClosed(state.Run, now, c.config.Idle) {
				state.FinalMetrics = true
			}
		}
	}
	c.startup = false
	return nil
}
