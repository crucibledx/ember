package ember

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

type spanEntry struct {
	span                      ptrace.Span
	resource                  pcommon.Resource
	adapter                   harness.Adapter
	mapping                   harness.Mapping
	registry                  *registry.Registry
	key                       registry.Key
	snapshot                  registry.Snapshot
	known                     bool
	session                   string
	agent                     string
	call                      string
	oldTrace                  pcommon.TraceID
	oldSpan                   pcommon.SpanID
	parent                    int
	triedComposite, triedBase bool
	nativeID                  pcommon.SpanID
	fileLink                  *registry.SpanRef
	placement                 placement
	state                     uint8
}

type traceGroup struct {
	entries []*spanEntry
	bySpan  map[pcommon.SpanID]int
}

type placement struct {
	moved     bool
	trace     pcommon.TraceID
	newSpan   pcommon.SpanID
	parent    pcommon.SpanID
	hasParent bool
}

func (p *emberProcessor) processTraces(ctx context.Context, traces ptrace.Traces) error {
	groups := map[pcommon.TraceID]*traceGroup{}
	var order []*traceGroup
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		rs := traces.ResourceSpans().At(i)
		adapter, matched := matchResource(p.adapters, rs.Resource())
		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			spans := rs.ScopeSpans().At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				if !matched {
					continue
				}
				trace := span.TraceID()
				group := groups[trace]
				if group == nil {
					group = &traceGroup{bySpan: map[pcommon.SpanID]int{}}
					groups[trace], order = group, append(order, group)
				}
				entry := &spanEntry{span: span, resource: rs.Resource(), adapter: adapter,
					mapping: adapter.Mapping(), registry: p.registry, oldTrace: trace, oldSpan: span.SpanID(), parent: -1}
				group.bySpan[entry.oldSpan] = len(group.entries)
				group.entries = append(group.entries, entry)
			}
		}
	}
	for _, group := range order {
		p.reparentGroup(ctx, group)
	}
	for _, group := range order {
		for _, entry := range group.entries {
			if entry.known {
				p.remapLinks(entry)
			}
		}
	}
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		rs := traces.ResourceSpans().At(i)
		adapter, matched := matchResource(p.adapters, rs.Resource())
		if err := p.privacyTraces(rs, adapter, matched); err != nil {
			return err
		}
	}
	return nil
}

func (p *emberProcessor) reparentGroup(ctx context.Context, group *traceGroup) {
	for i, entry := range group.entries {
		attrs, resource := entry.span.Attributes(), entry.resource.Attributes()
		entry.session = attribute(attrs, resource, entry.mapping.SessionKey)
		entry.agent = attribute(attrs, resource, entry.mapping.SubagentKey)
		entry.call = attribute(attrs, resource, entry.mapping.ToolCallKeys...)
		if parent := entry.span.ParentSpanID(); !parent.IsEmpty() {
			if index, ok := group.bySpan[parent]; ok && index != i {
				entry.parent = index
			}
		}
	}
	p.resolveGroup(ctx, group.entries)
	for _, entry := range group.entries {
		if !entry.known {
			p.registry.IncUnresolved()
			continue
		}
		p.registry.NoteNative(entry.key, entry.span.StartTimestamp().AsTime())
		p.prepareTool(entry)
		p.enrichSpan(entry)
		restoreAndSplitTool(entry, p.config.Privacy.RestoreToolNames)
	}
	for i := range group.entries {
		if group.entries[i].known {
			p.place(group, i)
		}
	}
	for _, entry := range group.entries {
		if entry.known {
			p.moveSpan(entry)
		}
	}
}

func (p *emberProcessor) resolveGroup(ctx context.Context, entries []*spanEntry) {
	for range len(entries) + 1 {
		changed := false
		for _, entry := range entries {
			if entry.known {
				continue
			}
			if p.resolveEntry(ctx, entry, entries) {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	var root *spanEntry
	for _, entry := range entries {
		if entry.known && (entry.span.ParentSpanID().IsEmpty() || entry.parent == -1) {
			root = entry
			break
		}
	}
	if root != nil {
		for _, entry := range entries {
			if !entry.known && entry.session == "" {
				p.selectRun(ctx, entry, root.key)
			}
		}
	}
}

func (p *emberProcessor) selectRun(ctx context.Context, entry *spanEntry, key registry.Key) bool {
	snapshot, ok := p.resolve(ctx, key, entry.span.StartTimestamp().AsTime())
	if ok {
		entry.key, entry.snapshot, entry.known = key, snapshot, true
	}
	return ok
}

func (p *emberProcessor) resolveEntry(ctx context.Context, entry *spanEntry, entries []*spanEntry) bool {
	if entry.session != "" && entry.agent != "" && !entry.triedComposite {
		entry.triedComposite = true
		key := registry.Key{Harness: entry.adapter.Name(), RunID: entry.session + "/" + entry.agent}
		if p.selectRun(ctx, entry, key) {
			return true
		}
	}
	if entry.call != "" {
		if owner, ok := p.registry.ToolOwner(entry.call); ok && owner.Harness == entry.adapter.Name() && candidateMatches(entry.session, owner) {
			if p.selectRun(ctx, entry, owner) {
				return true
			}
		}
	}
	if entry.parent >= 0 && entries[entry.parent].known && candidateMatches(entry.session, entries[entry.parent].key) {
		if p.selectRun(ctx, entry, entries[entry.parent].key) {
			return true
		}
	}
	if entry.parent == -1 && !entry.span.ParentSpanID().IsEmpty() {
		if parent, ok := p.cache.get(spanKey{trace: entry.oldTrace, span: entry.span.ParentSpanID()}); ok && candidateMatches(entry.session, parent.run) {
			if p.selectRun(ctx, entry, parent.run) {
				return true
			}
		}
	}
	if entry.session != "" && !entry.triedBase {
		entry.triedBase = true
		return p.selectRun(ctx, entry, registry.Key{Harness: entry.adapter.Name(), RunID: entry.session})
	}
	return false
}

func isToolSpan(mapping harness.Mapping, name, call string) bool {
	pattern := mapping.ToolSpanName
	return call != "" && pattern != "" && (name == pattern || strings.HasSuffix(pattern, " ") && strings.HasPrefix(name, pattern))
}

func (p *emberProcessor) prepareTool(entry *spanEntry) {
	entry.nativeID = entry.oldSpan
	if !isToolSpan(entry.mapping, entry.span.Name(), entry.call) {
		return
	}
	id, file, linked := p.registry.NativeTool(entry.key, entry.call, spanString(entry.oldSpan))
	if decoded, ok := decodeSpan(id); ok {
		entry.nativeID = decoded
	}
	if linked {
		if file.TraceID == "" {
			file.TraceID = entry.snapshot.TraceID
		}
		entry.fileLink = &file
	}
}

func (p *emberProcessor) place(group *traceGroup, index int) placement {
	entry := group.entries[index]
	if entry.state == 2 {
		return entry.placement
	}
	if entry.state == 1 {
		return externalPlacement(entry)
	}
	entry.state = 1
	entry.placement = p.calculatePlacement(group, index)
	entry.state = 2
	return entry.placement
}

func (p *emberProcessor) calculatePlacement(group *traceGroup, index int) placement {
	entry := group.entries[index]
	if entry.parent >= 0 && group.entries[entry.parent].known && group.entries[entry.parent].key == entry.key {
		parent := p.place(group, entry.parent)
		if !parent.moved {
			return externalPlacement(entry)
		}
		return placement{moved: true, trace: parent.trace, newSpan: entry.nativeID, parent: parent.newSpan, hasParent: true}
	}
	if entry.span.ParentSpanID().IsEmpty() {
		return ownPlacement(entry)
	}
	if entry.parent >= 0 {
		parentEntry := group.entries[entry.parent]
		parent := p.place(group, entry.parent)
		if parentEntry.known && parent.moved {
			p.recordParent(entry, parentEntry.key, parentEntry.call)
			return placement{moved: true, trace: parent.trace, newSpan: entry.nativeID, parent: parent.newSpan, hasParent: true}
		}
		return externalPlacement(entry)
	}
	parent, ok := p.cache.get(spanKey{trace: entry.oldTrace, span: entry.span.ParentSpanID()})
	if !ok || !parent.moved {
		return externalPlacement(entry)
	}
	p.recordParent(entry, parent.run, parent.toolID)
	return placement{moved: true, trace: parent.newTrace, newSpan: entry.nativeID, parent: parent.newSpan, hasParent: true}
}

func ownPlacement(entry *spanEntry) placement {
	trace, ok := decodeTrace(entry.snapshot.TraceID)
	if !ok {
		return externalPlacement(entry)
	}
	result := placement{moved: true, trace: trace, newSpan: entry.nativeID}
	if parent, ok := segmentSpan(entry.snapshot, entry.span.StartTimestamp().AsTime()); ok {
		result.parent, result.hasParent = parent, true
	}
	return result
}
func externalPlacement(entry *spanEntry) placement {
	return placement{trace: entry.oldTrace, newSpan: entry.oldSpan, parent: entry.span.ParentSpanID(), hasParent: !entry.span.ParentSpanID().IsEmpty()}
}
func (p *emberProcessor) recordParent(entry *spanEntry, parent registry.Key, call string) {
	if parent != entry.key {
		p.registry.RecordParent(entry.key, parent, call)
	}
}

func (p *emberProcessor) moveSpan(entry *spanEntry) {
	placement := entry.placement
	if placement.moved {
		entry.span.SetTraceID(placement.trace)
		entry.span.SetSpanID(placement.newSpan)
		if placement.hasParent {
			entry.span.SetParentSpanID(placement.parent)
		} else {
			entry.span.SetParentSpanID(pcommon.SpanID{})
		}
		entry.span.Attributes().PutStr("ember.original_trace_id", traceString(entry.oldTrace))
	}
	p.cache.put(spanKey{trace: entry.oldTrace, span: entry.oldSpan}, remap{
		moved: placement.moved, run: entry.key, snapshot: entry.snapshot, toolID: entry.call,
		newTrace: placement.trace, newSpan: placement.newSpan,
	})
}

func (p *emberProcessor) remapLinks(entry *spanEntry) {
	if entry.placement.moved {
		links := entry.span.Links()
		for i := 0; i < links.Len(); i++ {
			link := links.At(i)
			if target, ok := p.cache.get(spanKey{trace: link.TraceID(), span: link.SpanID()}); ok && target.moved {
				link.SetTraceID(target.newTrace)
				link.SetSpanID(target.newSpan)
			}
		}
	}
	if entry.fileLink == nil {
		return
	}
	trace, traceOK := decodeTrace(entry.fileLink.TraceID)
	span, spanOK := decodeSpan(entry.fileLink.SpanID)
	if !traceOK || !spanOK {
		return
	}
	link := entry.span.Links().AppendEmpty()
	link.SetTraceID(trace)
	link.SetSpanID(span)
	link.Attributes().PutStr("ember.link.type", "file_tool_span")
}
