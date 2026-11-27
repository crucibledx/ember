package emit

import (
	"cmp"
	"fmt"
	"sort"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

const (
	AgentStarted = "ember.agent.started"
	AgentEnded   = "ember.agent.ended"
	LLMTurn      = "ember.llm.turn"
	ToolCall     = "ember.tool.call"
)

type logEntry struct {
	at       time.Time
	event    string
	body     string
	severity plog.SeverityNumber
	spanID   string
	call     *harness.ToolCall
	turn     int
}

func Logs(forest *tree.Forest, config Config) plog.Logs {
	out := plog.NewLogs()
	for _, node := range forest.Ordered() {
		if node.Held {
			continue
		}
		resource := out.ResourceLogs().AppendEmpty()
		resourceFor(resource.Resource(), node, config)
		scope := resource.ScopeLogs().AppendEmpty()
		scope.Scope().SetName("ember")
		scope.Scope().SetVersion(config.Resource.Version)
		for _, entry := range logEntries(node) {
			appendLog(scope.LogRecords().AppendEmpty(), node, entry)
		}
	}
	return out
}
func logEntries(node *tree.Node) []logEntry {
	run := node.Run
	entries := []logEntry{{
		at: run.StartedAt, event: AgentStarted, severity: plog.SeverityNumberInfo,
		body:   fmt.Sprintf("agent %s started (%s)", agentName(run), run.Kind),
		spanID: tree.SessionSpanID(run.Harness, run.SessionID),
	}}
	for i, at := range run.TurnTimes {
		entries = append(entries, logEntry{at: at, event: LLMTurn, body: fmt.Sprintf("llm turn %d", i+1),
			severity: plog.SeverityNumberInfo, turn: i + 1, spanID: segmentIDAt(node, at)})
	}
	for i := range run.ToolCalls {
		call := &run.ToolCalls[i]
		if !toolEligible(node, *call) {
			continue
		}
		at := call.EndedAt
		if at.IsZero() {
			at = call.StartedAt
		}
		outcome, duration := "incomplete", ""
		if !call.EndedAt.IsZero() {
			outcome = "ok"
			duration = fmt.Sprintf(" %dms", call.EndedAt.Sub(call.StartedAt).Milliseconds())
		}
		severity := plog.SeverityNumberInfo
		if call.IsError {
			severity = plog.SeverityNumberError
			outcome = "error"
		}
		entries = append(entries, logEntry{at: at, event: ToolCall, severity: severity, call: call,
			body:   fmt.Sprintf("tool %s %s%s", call.Name, outcome, duration),
			spanID: tree.ToolSpanID(run.Harness, call.ID)})
	}
	end := run.EndedAt
	if !tree.Terminal(run.Status) {
		end = tree.LastRecord(run)
	}
	if end.Before(run.StartedAt) {
		end = run.StartedAt
	}
	severity := plog.SeverityNumberInfo
	if run.Status == harness.StatusFailed {
		severity = plog.SeverityNumberError
	}
	if !runOpen(node) {
		entries = append(entries, logEntry{at: end, event: AgentEnded, severity: severity,
			body:   fmt.Sprintf("agent %s ended: %s, %d tool calls, %d llm turns", agentName(run), run.Status, len(run.ToolCalls), run.LLMTurns),
			spanID: segmentIDAt(node, end)})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	return entries
}
func appendLog(record plog.LogRecord, node *tree.Node, entry logEntry) {
	run := node.Run
	record.SetTimestamp(timestamp(entry.at))
	record.SetObservedTimestamp(timestamp(entry.at))
	record.SetSeverityNumber(entry.severity)
	record.SetSeverityText("INFO")
	if entry.severity == plog.SeverityNumberError {
		record.SetSeverityText("ERROR")
	}
	record.Body().SetStr(entry.body)
	record.SetTraceID(traceID(node.TraceID()))
	record.SetSpanID(spanID(entry.spanID))
	attrs := record.Attributes()
	putStrings(attrs, "event.name", entry.event, "session.id", run.SessionID,
		"gen_ai.agent.name", agentName(run), "ember.harness", run.Harness,
		"ember.agent.kind", string(run.Kind), "ember.workstream.id", node.WorkstreamID, "ember.work.id", node.WorkID)
	if entry.event == AgentStarted || entry.event == LLMTurn {
		putString(attrs, "gen_ai.request.model", run.Model)
	}
	if entry.turn != 0 {
		attrs.PutInt("ember.llm.turn", int64(entry.turn))
	}
	if entry.event == AgentEnded {
		attrs.PutStr("ember.agent.status", string(run.Status))
		if run.Status == harness.StatusFailed {
			attrs.PutStr("error.type", "agent_failed")
		}
	}
	if entry.call != nil {
		appendToolLog(attrs, *entry.call)
	}
}
func appendToolLog(attrs pcommon.Map, call harness.ToolCall) {
	putStrings(attrs, "gen_ai.tool.name", call.Name, "gen_ai.tool.call.id", call.ID)
	if call.IsError {
		attrs.PutStr("error.type", "tool_error")
	}
	if call.EndedAt.IsZero() {
		attrs.PutBool("ember.tool.incomplete", true)
	} else {
		attrs.PutInt("ember.tool.duration_ms", call.EndedAt.Sub(call.StartedAt).Milliseconds())
	}
}
func agentName(run harness.Run) string { return cmp.Or(run.AgentType, string(run.Kind)) }
func segmentIDAt(node *tree.Node, at time.Time) string {
	segment, ok := tree.SegmentAt(node.Segments, at, node.Idle)
	if !ok {
		return tree.SessionSpanID(node.Run.Harness, node.Run.SessionID)
	}
	return SegmentSpanID(node, segment.Number)
}
