package sessions

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/tree"
)

func (c *core) emitLogs(ctx context.Context, forest *tree.Forest, now time.Time) error {
	out := emit.Logs(forest, emit.Config{})
	ordinals := map[string]int{}
	for _, resource := range out.ResourceLogs().All() {
		runID, _ := resource.Resource().Attributes().Get("service.instance.id")
		state := c.state.Runs[runID.Str()]
		for _, scope := range resource.ScopeLogs().All() {
			scope.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
				at := record.Timestamp().AsTime()
				if !recent(state.Run, now, c.config.Lookback) || at.Before(state.LogTime) {
					return true
				}
				if at.Equal(state.LogTime) {
					ordinals[runID.Str()]++
					return ordinals[runID.Str()] <= state.LogOrdinal
				}
				return false
			})
		}
	}
	out.ResourceLogs().RemoveIf(func(r plog.ResourceLogs) bool {
		n := 0
		for _, s := range r.ScopeLogs().All() {
			n += s.LogRecords().Len()
		}
		return n == 0
	})
	if out.LogRecordCount() == 0 {
		return nil
	}
	if c.logs == nil {
		return nil
	}
	if err := c.logs.ConsumeLogs(ctx, out); err != nil {
		return err
	}
	for _, resource := range out.ResourceLogs().All() {
		runID, _ := resource.Resource().Attributes().Get("service.instance.id")
		state := c.state.Runs[runID.Str()]
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				at := record.Timestamp().AsTime()
				if at.After(state.LogTime) {
					state.LogTime = at
					state.LogOrdinal = 1
				} else if at.Equal(state.LogTime) {
					state.LogOrdinal++
				}
			}
		}
	}
	return nil
}
