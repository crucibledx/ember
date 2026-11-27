package sessions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/tree"
)

func logResourceState(c *core, resource plog.ResourceLogs) *runState {
	runID, _ := resource.Resource().Attributes().Get("service.instance.id")
	for _, scope := range resource.ScopeLogs().All() {
		for _, record := range scope.LogRecords().All() {
			harnessName, _ := record.Attributes().Get("ember.harness")
			return c.state.Runs[stateKey(harnessName.Str(), runID.Str())]
		}
	}
	return nil
}
func pruneLogs(out plog.Logs) {
	out.ResourceLogs().RemoveIf(func(r plog.ResourceLogs) bool {
		n := 0
		for _, s := range r.ScopeLogs().All() {
			n += s.LogRecords().Len()
		}
		return n == 0
	})
}
func logIdentity(resource plog.ResourceLogs, record plog.LogRecord) string {
	runID, _ := resource.Resource().Attributes().Get("service.instance.id")
	harnessName, _ := record.Attributes().Get("ember.harness")
	event, _ := record.Attributes().Get("event.name")
	call, _ := record.Attributes().Get("gen_ai.tool.call.id")
	return fmt.Sprintf("%s/%d/%s/%s/%s/%s", stateKey(harnessName.Str(), runID.Str()), record.Timestamp(), event.Str(), record.SpanID().String(), call.Str(), record.Body().AsString())
}
func acceptedLogs(out, failed plog.Logs) plog.Logs {
	failedIDs := map[string]bool{}
	for _, resource := range failed.ResourceLogs().All() {
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				failedIDs[logIdentity(resource, record)] = true
			}
		}
	}
	accepted := plog.NewLogs()
	out.CopyTo(accepted)
	for _, resource := range accepted.ResourceLogs().All() {
		for _, scope := range resource.ScopeLogs().All() {
			scope.LogRecords().RemoveIf(func(record plog.LogRecord) bool { return failedIDs[logIdentity(resource, record)] })
		}
	}
	pruneLogs(accepted)
	return accepted
}
func logOffset(at time.Time, ordinal int) string { return fmt.Sprintf("%d:%d", at.UnixNano(), ordinal) }
func (c *core) emitLogs(ctx context.Context, forest *tree.Forest, now time.Time) error {
	out := emit.Logs(forest, emit.Config{})
	for _, resource := range out.ResourceLogs().All() {
		state := logResourceState(c, resource)
		if state == nil {
			continue
		}
		ordinals := map[int64]int{}
		for _, scope := range resource.ScopeLogs().All() {
			scope.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
				at := record.Timestamp().AsTime()
				ordinals[at.UnixNano()]++
				if !recent(state.Run, now, c.lookback()) {
					return true
				}
				if state.AcceptedLogs != nil {
					return slices.Contains(state.AcceptedLogs, logOffset(at, ordinals[at.UnixNano()]))
				}
				if at.Before(state.LogTime) {
					return true
				}
				return at.Equal(state.LogTime) && ordinals[at.UnixNano()] <= state.LogOrdinal
			})
		}
	}
	pruneLogs(out)
	if out.LogRecordCount() == 0 {
		return nil
	}
	accepted := out
	err := c.logs.ConsumeLogs(ctx, out)
	if err != nil {
		var partial consumererror.Logs
		if errors.As(err, &partial) {
			accepted = acceptedLogs(out, partial.Data())
		} else {
			accepted = plog.NewLogs()
		}
	}
	c.commitLogs(out, accepted)
	return err
}
func (c *core) commitLogs(out, accepted plog.Logs) {
	acceptedIDs := map[string]bool{}
	for _, resource := range accepted.ResourceLogs().All() {
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				acceptedIDs[logIdentity(resource, record)] = true
			}
		}
	}
	for _, resource := range out.ResourceLogs().All() {
		state := logResourceState(c, resource)
		if state == nil {
			continue
		}
		if state.AcceptedLogs == nil {
			state.AcceptedLogs = []string{}
		}
		ordinals := map[int64]int{}
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				at := record.Timestamp().AsTime()
				ordinals[at.UnixNano()]++
				if !acceptedIDs[logIdentity(resource, record)] {
					continue
				}
				key := logOffset(at, ordinals[at.UnixNano()])
				if !slices.Contains(state.AcceptedLogs, key) {
					state.AcceptedLogs = append(state.AcceptedLogs, key)
				}
			}
		}
		slices.Sort(state.AcceptedLogs)
	}
}
