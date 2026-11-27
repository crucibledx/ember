package ember

import (
	"context"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

func snapshotAttribution(snapshot registry.Snapshot) (string, string) {
	return snapshot.WorkstreamID, snapshot.StartedBy
}

func enrich(attrs pcommon.Map, snapshot registry.Snapshot) {
	workstream, started := snapshotAttribution(snapshot)
	putNonempty(attrs, "ember.harness", snapshot.Run.Harness)
	putNonempty(attrs, "ember.agent.kind", string(snapshot.Run.Kind))
	putNonempty(attrs, "ember.workstream.id", workstream)
	putNonempty(attrs, "ember.work.id", snapshot.WorkID)
	putNonempty(attrs, "ember.human.started_by", started)
}

func putNonempty(attrs pcommon.Map, key, value string) {
	if value != "" {
		attrs.PutStr(key, value)
	}
}

func (p *emberProcessor) noteOwner(snapshot *registry.Snapshot, key registry.Key, mapping harness.Mapping, attrs, resource pcommon.Map) {
	owner := attribute(attrs, resource, mapping.OwnerKeys...)
	if owner == "" {
		return
	}
	p.registry.NoteOwner(key, owner)
	if snapshot.Run.Owner == "" {
		snapshot.Run.Owner = owner
		if snapshot.StartedBy == "" {
			snapshot.StartedBy = snapshot.Run.Harness + ":" + owner
		}
	}
}

func segmentSpan(snapshot registry.Snapshot, at time.Time) (pcommon.SpanID, bool) {
	segment, ok := tree.SegmentAt(snapshot.Segments, at, snapshot.Idle)
	if !ok {
		return pcommon.SpanID{}, false
	}
	id := emit.SegmentSpanID(&tree.Node{Run: snapshot.Run}, segment.Number)
	return decodeSpan(id)
}

func (p *emberProcessor) processLogs(ctx context.Context, logs plog.Logs) error {
	for i := 0; i < logs.ResourceLogs().Len(); i++ {
		rl := logs.ResourceLogs().At(i)
		adapter, matched := matchResource(p.adapters, rl.Resource())
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				record := sl.LogRecords().At(k)
				if matched {
					p.processLog(ctx, adapter, rl.Resource(), record)
				}
			}
		}
		if err := p.privacyLogs(rl, adapter, matched); err != nil {
			return err
		}
	}
	return nil
}

func (p *emberProcessor) processLog(ctx context.Context, adapter harness.Adapter, resource pcommon.Resource, record plog.LogRecord) {
	at := record.Timestamp().AsTime()
	if at.IsZero() {
		at = record.ObservedTimestamp().AsTime()
	}
	snapshot, key, ok := p.resolveData(ctx, adapter, record.Attributes(), resource.Attributes(), at)
	if !ok {
		return
	}
	p.noteOwner(&snapshot, key, adapter.Mapping(), record.Attributes(), resource.Attributes())
	enrich(record.Attributes(), snapshot)
	if !record.TraceID().IsEmpty() {
		return
	}
	if trace, ok := decodeTrace(snapshot.TraceID); ok {
		record.SetTraceID(trace)
	}
	mapping := adapter.Mapping()
	if call := attribute(record.Attributes(), resource.Attributes(), mapping.ToolCallKeys...); call != "" {
		if id, ok := decodeSpan(tree.ToolSpanID(snapshot.Run.Harness, call)); ok {
			record.SetSpanID(id)
		}
	} else if id, ok := segmentSpan(snapshot, at); ok {
		record.SetSpanID(id)
	}
}

func (p *emberProcessor) processMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
		rm := metrics.ResourceMetrics().At(i)
		adapter, matched := matchResource(p.adapters, rm.Resource())
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			sm := rm.ScopeMetrics().At(j)
			for k := 0; k < sm.Metrics().Len(); k++ {
				if matched {
					p.processMetric(ctx, adapter, rm.Resource(), sm.Metrics().At(k))
				}
			}
		}
		if err := p.privacyMetrics(rm, adapter, matched); err != nil {
			return err
		}
	}
	return nil
}

func (p *emberProcessor) processMetric(ctx context.Context, adapter harness.Adapter, resource pcommon.Resource, metric pmetric.Metric) {
	visitPoints(metric, func(attrs pcommon.Map, at time.Time) {
		snapshot, key, ok := p.resolveData(ctx, adapter, attrs, resource.Attributes(), at)
		if !ok {
			return
		}
		p.noteOwner(&snapshot, key, adapter.Mapping(), attrs, resource.Attributes())
		enrich(attrs, snapshot)
	})
}

func visitPoints(metric pmetric.Metric, visit func(pcommon.Map, time.Time)) {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		for i := 0; i < metric.Gauge().DataPoints().Len(); i++ {
			point := metric.Gauge().DataPoints().At(i)
			visit(point.Attributes(), point.Timestamp().AsTime())
		}
	case pmetric.MetricTypeSum:
		for i := 0; i < metric.Sum().DataPoints().Len(); i++ {
			point := metric.Sum().DataPoints().At(i)
			visit(point.Attributes(), point.Timestamp().AsTime())
		}
	case pmetric.MetricTypeHistogram:
		for i := 0; i < metric.Histogram().DataPoints().Len(); i++ {
			point := metric.Histogram().DataPoints().At(i)
			visit(point.Attributes(), point.Timestamp().AsTime())
		}
	case pmetric.MetricTypeExponentialHistogram:
		for i := 0; i < metric.ExponentialHistogram().DataPoints().Len(); i++ {
			point := metric.ExponentialHistogram().DataPoints().At(i)
			visit(point.Attributes(), point.Timestamp().AsTime())
		}
	case pmetric.MetricTypeSummary:
		for i := 0; i < metric.Summary().DataPoints().Len(); i++ {
			point := metric.Summary().DataPoints().At(i)
			visit(point.Attributes(), point.Timestamp().AsTime())
		}
	case pmetric.MetricTypeEmpty:
	}
}

func (p *emberProcessor) enrichSpan(entry *spanEntry) {
	snapshot := entry.snapshot
	p.noteOwner(&snapshot, entry.key, entry.mapping, entry.span.Attributes(), entry.resource.Attributes())
	entry.snapshot = snapshot
	enrich(entry.span.Attributes(), snapshot)
	if isToolSpan(entry.mapping, entry.span.Name(), entry.call) {
		call, ok := p.registry.ToolCall(entry.key, entry.call)
		if ok && len(call.SpawnedSessionIDs) != 0 {
			entry.span.Attributes().PutStr("ember.spawned.session_ids", strings.Join(call.SpawnedSessionIDs, ","))
		}
	}
}

func restoreAndSplitTool(entry *spanEntry, restore bool) {
	name := attribute(entry.span.Attributes(), entry.resource.Attributes(), entry.mapping.ToolNameKeys...)
	if name == "" {
		return
	}
	if restore && slices.Contains(entry.mapping.RedactedTools, name) {
		if call, ok := entry.registry.ToolCall(entry.key, entry.call); ok && call.Name != "" {
			replaceToolName(entry.span.Attributes(), entry.mapping.ToolNameKeys, name, call.Name)
			name = call.Name
		}
	}
	if entry.mapping.MCP == nil {
		return
	}
	match := entry.mapping.MCP.FindStringSubmatch(name)
	if len(match) >= 3 {
		entry.span.Attributes().PutStr("ember.mcp.server", match[1])
		entry.span.Attributes().PutStr("ember.mcp.tool", match[2])
	}
}

func replaceToolName(attrs pcommon.Map, keys []string, old, name string) {
	for _, key := range keys {
		if value, ok := attrs.Get(key); ok && value.Type() == pcommon.ValueTypeStr && value.Str() == old {
			value.SetStr(name)
			return
		}
	}
}
