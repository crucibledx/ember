package sessions

import (
	"context"
	"slices"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/tree"
)

type reservation struct {
	key  registry.Key
	call string
}

func (c *core) emitTraces(ctx context.Context, forest *tree.Forest, now time.Time) error {
	out := emit.Traces(forest, emit.Config{})
	var reserved []reservation
	for _, resource := range out.ResourceSpans().All() {
		runID, ok := resource.Resource().Attributes().Get("service.instance.id")
		if !ok {
			continue
		}
		state := c.state.Runs[runID.Str()]
		if state == nil {
			continue
		}
		key := registry.Key{Harness: state.Run.Harness, RunID: runID.Str()}
		for _, scope := range resource.ScopeSpans().All() {
			scope.Spans().RemoveIf(func(span ptrace.Span) bool {
				attrs := span.Attributes()
				segment, _ := attrs.Get("ember.session.segment")
				call, _ := attrs.Get("gen_ai.tool.call.id")
				if call.Str() == "" {
					return !recent(state.Run, now, c.config.Lookback) || int(segment.Int()) <= state.LastSegment
				}
				if slices.Contains(state.Tools, call.Str()) {
					return true
				}
				if !recent(state.Run, now, c.config.Lookback) {
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
	out.ResourceSpans().RemoveIf(func(r ptrace.ResourceSpans) bool {
		n := 0
		for _, s := range r.ScopeSpans().All() {
			n += s.Spans().Len()
		}
		return n == 0
	})
	if out.SpanCount() == 0 {
		return nil
	}
	if err := c.traces.ConsumeTraces(ctx, out); err != nil {
		for _, r := range reserved {
			c.registry.RollbackFile(r.key, r.call)
		}
		return err
	}
	for _, r := range reserved {
		c.registry.CommitFile(r.key, r.call)
	}
	for _, resource := range out.ResourceSpans().All() {
		runID, _ := resource.Resource().Attributes().Get("service.instance.id")
		state := c.state.Runs[runID.Str()]
		for _, scope := range resource.ScopeSpans().All() {
			for _, span := range scope.Spans().All() {
				if segment, ok := span.Attributes().Get("ember.session.segment"); ok && int(segment.Int()) > state.LastSegment {
					state.LastSegment = int(segment.Int())
				}
				if call, ok := span.Attributes().Get("gen_ai.tool.call.id"); ok && !slices.Contains(state.Tools, call.Str()) {
					state.Tools = append(state.Tools, call.Str())
				}
			}
		}
		state.Tools = sortedTools(state.Tools)
	}
	return nil
}
