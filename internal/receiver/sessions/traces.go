package sessions

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/tree"
)

type reservation struct {
	key  registry.Key
	call string
}

func traceResourceState(c *core, resource ptrace.ResourceSpans) *runState {
	runID, _ := resource.Resource().Attributes().Get("service.instance.id")
	for _, scope := range resource.ScopeSpans().All() {
		for _, span := range scope.Spans().All() {
			harnessName, _ := span.Attributes().Get("ember.harness")
			return c.state.Runs[stateKey(harnessName.Str(), runID.Str())]
		}
	}
	return nil
}
func pruneTraces(out ptrace.Traces) {
	out.ResourceSpans().RemoveIf(func(r ptrace.ResourceSpans) bool {
		n := 0
		for _, s := range r.ScopeSpans().All() {
			n += s.Spans().Len()
		}
		return n == 0
	})
}
func spanIdentity(span ptrace.Span) string {
	return span.TraceID().String() + "/" + span.SpanID().String()
}
func acceptedTraces(out, failed ptrace.Traces) ptrace.Traces {
	failedIDs := map[string]bool{}
	for _, resource := range failed.ResourceSpans().All() {
		for _, scope := range resource.ScopeSpans().All() {
			for _, span := range scope.Spans().All() {
				failedIDs[spanIdentity(span)] = true
			}
		}
	}
	accepted := ptrace.NewTraces()
	out.CopyTo(accepted)
	for _, resource := range accepted.ResourceSpans().All() {
		for _, scope := range resource.ScopeSpans().All() {
			scope.Spans().RemoveIf(func(span ptrace.Span) bool { return failedIDs[spanIdentity(span)] })
		}
	}
	pruneTraces(accepted)
	return accepted
}
func segmentEmitted(state *runState, number int) bool {
	return number <= state.LastSegment || slices.Contains(state.EmittedSegments, number)
}
func (c *core) emitTraces(ctx context.Context, forest *tree.Forest, now time.Time) error {
	out := emit.Traces(forest, emit.Config{})
	var reserved []reservation
	for _, resource := range out.ResourceSpans().All() {
		state := traceResourceState(c, resource)
		if state == nil {
			continue
		}
		key := registry.Key{Harness: state.Run.Harness, RunID: state.Run.SessionID}
		for _, scope := range resource.ScopeSpans().All() {
			scope.Spans().RemoveIf(func(span ptrace.Span) bool {
				attrs := span.Attributes()
				segment, _ := attrs.Get("ember.session.segment")
				call, _ := attrs.Get("gen_ai.tool.call.id")
				if call.Str() == "" {
					return !recent(state.Run, now, c.lookback()) || segmentEmitted(state, int(segment.Int()))
				}
				if slices.Contains(state.Tools, call.Str()) || !recent(state.Run, now, c.lookback()) {
					return true
				}
				_, ok := c.registry.ReserveFile(key, call.Str(), now, c.config.NativeGrace)
				if ok {
					reserved = append(reserved, reservation{key, call.Str()})
				}
				return !ok
			})
		}
	}
	pruneTraces(out)
	if out.SpanCount() == 0 {
		return nil
	}
	checkpoint := ptrace.NewTraces()
	out.CopyTo(checkpoint)
	accepted := checkpoint
	err := c.traces.ConsumeTraces(ctx, out)
	if err != nil {
		var partial consumererror.Traces
		if errors.As(err, &partial) {
			accepted = acceptedTraces(checkpoint, partial.Data())
		} else {
			accepted = ptrace.NewTraces()
		}
	}
	c.commitTraces(accepted, reserved)
	return err
}
func (c *core) commitTraces(out ptrace.Traces, reserved []reservation) {
	acceptedTools := map[string]bool{}
	for _, resource := range out.ResourceSpans().All() {
		state := traceResourceState(c, resource)
		if state == nil {
			continue
		}
		segments := map[int]bool{}
		for _, number := range state.EmittedSegments {
			segments[number] = true
		}
		for _, scope := range resource.ScopeSpans().All() {
			for _, span := range scope.Spans().All() {
				if segment, ok := span.Attributes().Get("ember.session.segment"); ok {
					number := int(segment.Int())
					segments[number] = true
					if !slices.Contains(state.EmittedSegments, number) {
						state.EmittedSegments = append(state.EmittedSegments, number)
					}
				}
				if call, ok := span.Attributes().Get("gen_ai.tool.call.id"); ok {
					key := registry.Key{Harness: state.Run.Harness, RunID: state.Run.SessionID}
					acceptedTools[stateKey(key.Harness, key.RunID)+call.Str()] = true
					if !slices.Contains(state.Tools, call.Str()) {
						state.Tools = append(state.Tools, call.Str())
					}
				}
			}
		}
		for segments[state.LastSegment+1] {
			state.LastSegment++
		}
		slices.Sort(state.EmittedSegments)
		state.Tools = sortedTools(state.Tools)
	}
	for _, item := range reserved {
		if acceptedTools[stateKey(item.key.Harness, item.key.RunID)+item.call] {
			c.registry.CommitFile(item.key, item.call)
		} else {
			c.registry.RollbackFile(item.key, item.call)
		}
	}
}
