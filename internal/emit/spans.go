package emit

import (
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

func Traces(forest *tree.Forest, config Config) ptrace.Traces {
	out := ptrace.NewTraces()
	for _, node := range forest.Ordered() {
		if node.Held {
			continue
		}
		resource := out.ResourceSpans().AppendEmpty()
		resourceFor(resource.Resource(), node, config)
		spans := resource.ScopeSpans().AppendEmpty()
		spans.Scope().SetName("ember")
		spans.Scope().SetVersion(config.Resource.Version)
		for _, segment := range node.Segments {
			if segment.Closed {
				appendSegment(spans.Spans().AppendEmpty(), node, segment)
			}
		}
		for _, call := range node.Run.ToolCalls {
			if len(node.Segments) != 0 && toolEligible(node.Run, call) {
				appendTool(spans.Spans().AppendEmpty(), node, call, config)
			}
		}
	}
	return out
}
func appendSegment(span ptrace.Span, node *tree.Node, segment tree.Segment) {
	run := node.Run
	span.SetTraceID(traceID(node.TraceID()))
	span.SetSpanID(spanID(SegmentSpanID(node, segment.Number)))
	if parent := node.ParentSpanID(); segment.Number == 1 && parent != "" {
		span.SetParentSpanID(spanID(parent))
	} else if segment.Number > 1 {
		span.SetParentSpanID(spanID(tree.SessionSpanID(run.Harness, run.SessionID)))
	}
	name := run.AgentType
	if name == "" {
		name = string(run.Kind)
	}
	span.SetName("invoke_agent " + name)
	span.SetKind(ptrace.SpanKindInternal)
	span.SetStartTimestamp(timestamp(segment.Start))
	span.SetEndTimestamp(timestamp(segment.End))
	attrs := span.Attributes()
	putStrings(attrs, "session.id", run.SessionID, "gen_ai.operation.name", "invoke_agent",
		"gen_ai.conversation.id", run.SessionID, "gen_ai.agent.name", name, "gen_ai.request.model", run.Model,
		"ember.harness", run.Harness, "ember.agent.kind", string(run.Kind),
		"ember.workstream.id", node.WorkstreamID, "ember.work.id", node.WorkID)
	if node.Parent != nil {
		putString(attrs, "ember.parent.session.id", node.Parent.SessionID)
		putString(attrs, "ember.parent.tool_call.id", node.Parent.ToolCallID)
	}
	putStrings(attrs, "ember.human.started_by", node.StartedBy, "ember.human.active", tree.ActiveHumanIDs(node.Active))
	attrs.PutInt("ember.session.segment", int64(segment.Number))
	attrs.PutInt("ember.agent.llm_turns", int64(segment.LLMTurns))
	attrs.PutInt("ember.agent.tool_calls", int64(segment.ToolCalls))
}
func appendTool(span ptrace.Span, node *tree.Node, call harness.ToolCall, config Config) {
	run := node.Run
	span.SetTraceID(traceID(node.TraceID()))
	span.SetSpanID(spanID(tree.ToolSpanID(run.Harness, call.ID)))
	segment, ok := tree.SegmentAt(node.Segments, call.StartedAt, node.Idle)
	if !ok {
		return
	}
	span.SetParentSpanID(spanID(SegmentSpanID(node, segment.Number)))
	span.SetName("execute_tool " + call.Name)
	span.SetKind(ptrace.SpanKindInternal)
	span.SetStartTimestamp(timestamp(call.StartedAt))
	end := call.EndedAt
	if end.IsZero() {
		end = run.EndedAt
	}
	if end.Before(call.StartedAt) {
		end = call.StartedAt
	}
	span.SetEndTimestamp(timestamp(end))
	attrs := span.Attributes()
	putStrings(attrs, "session.id", run.SessionID, "gen_ai.operation.name", "execute_tool",
		"gen_ai.tool.name", call.Name, "gen_ai.tool.call.id", call.ID,
		"ember.harness", run.Harness, "ember.work.id", node.WorkID)
	if call.EndedAt.IsZero() {
		attrs.PutBool("ember.tool.incomplete", true)
	}
	if call.IsError {
		attrs.PutStr("error.type", "tool_error")
		span.Status().SetCode(ptrace.StatusCodeError)
	}
	server, tool := mcpTool(config, node, call.Name)
	putStrings(attrs, "ember.mcp.server", server, "ember.mcp.tool", tool, "ember.spawned.session_ids", spawnedIDs(call.SpawnedSessionIDs))
}
