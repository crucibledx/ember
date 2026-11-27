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
	turn, _ := record.Attributes().Get("ember.llm.turn")
	return fmt.Sprintf("%q/%d/%q/%q/%q/%d", stateKey(harnessName.Str(), runID.Str()), record.Timestamp(), event.Str(), record.SpanID().String(), call.Str(), turn.Int())
}
func logCounts(out plog.Logs) map[string]int {
	counts := map[string]int{}
	for _, resource := range out.ResourceLogs().All() {
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				counts[logIdentity(resource, record)]++
			}
		}
	}
	return counts
}
func logExportIdentity(record plog.LogRecord) string {
	return fmt.Sprintf("%s/%s/%d", record.TraceID(), record.SpanID(), record.Timestamp())
}
func acceptedLogCounts(out, failed plog.Logs) map[string]int {
	accepted := logCounts(out)
	candidates := map[string]map[string]bool{}
	for _, resource := range out.ResourceLogs().All() {
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				exportID := logExportIdentity(record)
				if candidates[exportID] == nil {
					candidates[exportID] = map[string]bool{}
				}
				candidates[exportID][logIdentity(resource, record)] = true
			}
		}
	}
	for _, resource := range failed.ResourceLogs().All() {
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				identity := logIdentity(resource, record)
				if accepted[identity] > 0 {
					accepted[identity]--
					continue
				}
				matches := candidates[logExportIdentity(record)]
				if len(matches) == 0 {
					return map[string]int{}
				}
				for candidate := range matches {
					accepted[candidate] = 0
				}
			}
		}
	}
	return accepted
}
func logCheckpoint(identity string, occurrence int) string {
	return fmt.Sprintf("%s#%d", identity, occurrence)
}
func (c *core) emitLogs(ctx context.Context, forest *tree.Forest, now time.Time) error {
	out := emit.Logs(forest, emit.Config{})
	original := plog.NewLogs()
	out.CopyTo(original)
	for _, resource := range out.ResourceLogs().All() {
		state := logResourceState(c, resource)
		if state == nil {
			continue
		}
		ordinals := map[int64]int{}
		occurrences := map[string]int{}
		for _, scope := range resource.ScopeLogs().All() {
			scope.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
				at := record.Timestamp().AsTime()
				ordinals[at.UnixNano()]++
				identity := logIdentity(resource, record)
				occurrences[identity]++
				if !recent(state.Run, now, c.lookback()) {
					return true
				}
				if state.AcceptedLogs != nil {
					return slices.Contains(state.AcceptedLogs, logCheckpoint(identity, occurrences[identity]))
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
	checkpoint := plog.NewLogs()
	out.CopyTo(checkpoint)
	accepted := logCounts(checkpoint)
	err := c.logs.ConsumeLogs(ctx, out)
	if err != nil {
		var partial consumererror.Logs
		if errors.As(err, &partial) {
			accepted = acceptedLogCounts(checkpoint, partial.Data())
		} else {
			accepted = map[string]int{}
		}
	}
	c.commitLogs(original, accepted)
	return err
}
func (c *core) commitLogs(out plog.Logs, accepted map[string]int) {
	for _, resource := range out.ResourceLogs().All() {
		state := logResourceState(c, resource)
		if state == nil {
			continue
		}
		if state.AcceptedLogs == nil {
			state.AcceptedLogs = []string{}
		}
		occurrences := map[string]int{}
		for _, scope := range resource.ScopeLogs().All() {
			for _, record := range scope.LogRecords().All() {
				identity := logIdentity(resource, record)
				occurrences[identity]++
				key := logCheckpoint(identity, occurrences[identity])
				if slices.Contains(state.AcceptedLogs, key) || accepted[identity] == 0 {
					continue
				}
				state.AcceptedLogs = append(state.AcceptedLogs, key)
				accepted[identity]--
			}
		}
		slices.Sort(state.AcceptedLogs)
	}
}
