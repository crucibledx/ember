package ember

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

func TestReparent(t *testing.T) {
	t.Run("same and later batch with links", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		snapshot := testSnapshot(p, "run")
		traces, resource := testTraces()
		root := addSpan(resource, testTrace(1), testSpan(1), pcommon.SpanID{}, "root", "run")
		child := addSpan(resource, testTrace(1), testSpan(3), testSpan(2), "child", "run")
		tool := addSpan(resource, testTrace(1), testSpan(2), testSpan(1), "tool invocation", "run")
		tool.Attributes().PutStr("call.id", "call-1")
		link := root.Links().AppendEmpty()
		link.SetTraceID(testTrace(1))
		link.SetSpanID(testSpan(2))
		processTestTraces(t, p, traces)

		newTrace, _ := decodeTrace(snapshot.TraceID)
		newTool, _ := decodeSpan(tree.ToolSpanID("synthetic", "call-1"))
		segment, _ := decodeSpan(tree.SessionSpanID("synthetic", "run"))
		spans := resource.ScopeSpans().At(0).Spans()
		if spans.At(0).TraceID() != newTrace || spans.At(0).ParentSpanID() != segment {
			t.Fatalf("root placement = %s/%s", spans.At(0).TraceID(), spans.At(0).ParentSpanID())
		}
		if spans.At(2).SpanID() != newTool || spans.At(1).ParentSpanID() != newTool {
			t.Fatalf("forward parent remap failed: tool=%s child parent=%s", spans.At(2).SpanID(), spans.At(1).ParentSpanID())
		}
		if spans.At(0).Links().At(0).SpanID() != newTool || stringAttr(spans.At(0).Attributes(), "ember.original_trace_id") != traceString(testTrace(1)) {
			t.Fatal("same-batch link or original trace was not remapped")
		}

		later, laterResource := testTraces()
		span := addSpan(laterResource, testTrace(1), testSpan(4), testSpan(3), "later", "run")
		laterLink := span.Links().AppendEmpty()
		laterLink.SetTraceID(testTrace(1))
		laterLink.SetSpanID(testSpan(2))
		processTestTraces(t, p, later)
		span = laterResource.ScopeSpans().At(0).Spans().At(0)
		if span.TraceID() != newTrace || span.ParentSpanID() != testSpan(3) || span.Links().At(0).SpanID() != newTool {
			t.Fatalf("later remap = %s parent=%s link=%s", span.TraceID(), span.ParentSpanID(), span.Links().At(0).SpanID())
		}
		_ = child
	})

	t.Run("segment discovery and reparse", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		seed := newTestProcessor(harness.Mapping{})
		snapshot := testSnapshot(seed, "late")
		snapshot.Segments = []tree.Segment{
			{Number: 1, Start: testBase, End: testBase.Add(time.Minute)},
			{Number: 2, Start: testBase.Add(10 * time.Minute), End: testBase.Add(20 * time.Minute)},
		}
		calls := 0
		p.resolveFn = func(_ context.Context, key registry.Key, _ time.Time) (registry.Snapshot, error) {
			calls++
			p.registry.Put(snapshot)
			return snapshot, nil
		}
		traces, resource := testTraces()
		span := addSpan(resource, testTrace(2), testSpan(1), pcommon.SpanID{}, "root", "late")
		span.SetStartTimestamp(pcommon.NewTimestampFromTime(testBase.Add(11 * time.Minute)))
		processTestTraces(t, p, traces)
		segment, _ := decodeSpan(tree.SegmentSpanID("synthetic", "late", 2))
		span = resource.ScopeSpans().At(0).Spans().At(0)
		if span.ParentSpanID() != segment || calls != 1 {
			t.Fatalf("discovery calls=%d parent=%s want=%s", calls, span.ParentSpanID(), segment)
		}

		old, _ := p.registry.Get(registry.Key{Harness: "synthetic", RunID: "late"})
		old.LastRecord = testBase.Add(12 * time.Minute)
		p.registry.Put(old)
		snapshot.TraceID = tree.TraceID("reparsed-root")
		snapshot.LastRecord = testBase.Add(time.Hour)
		later, laterResource := testTraces()
		span = addSpan(laterResource, testTrace(3), testSpan(2), pcommon.SpanID{}, "newer", "late")
		span.SetStartTimestamp(pcommon.NewTimestampFromTime(testBase.Add(13 * time.Minute)))
		processTestTraces(t, p, later)
		want, _ := decodeTrace(snapshot.TraceID)
		if got := laterResource.ScopeSpans().At(0).Spans().At(0).TraceID(); got != want || calls != 2 {
			t.Fatalf("reparse calls=%d trace=%s want=%s", calls, got, want)
		}
	})

	t.Run("mixed runs and distinct unknown session", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		parent := testSnapshot(p, "parent")
		parent.Run.ToolCalls = []harness.ToolCall{{ID: "parent-call", Name: "task"}}
		p.registry.Put(parent)
		child := testSnapshot(p, "parent/child")
		child.Run.ToolCalls = []harness.ToolCall{{ID: "spawn", Name: "task"}}
		p.registry.Put(child)
		traces, resource := testTraces()
		root := addSpan(resource, testTrace(4), testSpan(1), pcommon.SpanID{}, "tool parent", "parent")
		root.Attributes().PutStr("call.id", "parent-call")
		tool := addSpan(resource, testTrace(4), testSpan(2), testSpan(1), "tool spawn", "parent")
		tool.Attributes().PutStr("call.id", "spawn")
		execution := addSpan(resource, testTrace(4), testSpan(3), testSpan(2), "execution", "parent")
		execution.Attributes().PutStr("call.id", "spawn")
		unknown := addSpan(resource, testTrace(4), testSpan(4), testSpan(1), "distinct", "missing")
		processTestTraces(t, p, traces)
		spans := resource.ScopeSpans().At(0).Spans()
		wantTrace, _ := decodeTrace(parent.TraceID)
		wantTool, _ := decodeSpan(tree.ToolSpanID("synthetic", "spawn"))
		if spans.At(1).TraceID() != wantTrace || spans.At(2).ParentSpanID() != wantTool {
			t.Fatal("tool-owner precedence did not select the child run")
		}
		if unknown.TraceID() != testTrace(4) || unknown.ParentSpanID() != testSpan(1) {
			t.Fatal("distinct unknown session inherited the group root")
		}
		if registryCounter(p.registry, "unresolved") != 1 {
			t.Fatalf("unresolved = %d", registryCounter(p.registry, "unresolved"))
		}
		if key, call, ok := p.registry.Parent(registry.Key{Harness: "synthetic", RunID: "parent/child"}); !ok || key.RunID != "parent" || call != "parent-call" {
			t.Fatalf("recorded parent = %v %q %v", key, call, ok)
		}
	})

	t.Run("external and moved inbound parents", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		testSnapshot(p, "external")
		traces, resource := testTraces()
		span := addSpan(resource, testTrace(9), testSpan(1), testSpan(99), "external", "external")
		processTestTraces(t, p, traces)
		if span.TraceID() != testTrace(9) || span.SpanID() != testSpan(1) || span.ParentSpanID() != testSpan(99) ||
			stringAttr(span.Attributes(), "ember.original_trace_id") != "" || stringAttr(span.Attributes(), "ember.harness") == "" {
			t.Fatal("external inbound parent was not preserved and enriched")
		}

		testSnapshot(p, "parent")
		testSnapshot(p, "child")
		first, firstResource := testTraces()
		addSpan(firstResource, testTrace(5), testSpan(1), pcommon.SpanID{}, "parent", "parent")
		processTestTraces(t, p, first)
		second, secondResource := testTraces()
		child := addSpan(secondResource, testTrace(5), testSpan(2), testSpan(1), "child", "child")
		processTestTraces(t, p, second)
		if child.TraceID() != firstResource.ScopeSpans().At(0).Spans().At(0).TraceID() || child.ParentSpanID() != testSpan(1) {
			t.Fatal("child did not follow moved inbound parent")
		}
	})

	t.Run("native first link and cycles", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		snapshot := testSnapshot(p, "run")
		key := registry.Key{Harness: "synthetic", RunID: "run"}
		file := registry.SpanRef{TraceID: snapshot.TraceID, SpanID: tree.ToolSpanID("synthetic", "call")}
		p.registry.RestoreFile(key, "call", file)
		traces, resource := testTraces()
		span := addSpan(resource, testTrace(6), testSpan(2), pcommon.SpanID{}, "tool native", "run")
		span.Attributes().PutStr("call.id", "call")
		addSpan(resource, testTrace(6), testSpan(3), testSpan(4), "cycle-a", "run")
		addSpan(resource, testTrace(6), testSpan(4), testSpan(3), "cycle-b", "run")
		processTestTraces(t, p, traces)
		if span.SpanID() != testSpan(2) || span.Links().Len() != 1 {
			t.Fatalf("native id was not retained with a file link: %s links=%d", span.SpanID(), span.Links().Len())
		}
		link := span.Links().At(0)
		if link.TraceID().IsEmpty() || link.SpanID().IsEmpty() || stringAttr(link.Attributes(), "ember.link.type") != "file_tool_span" {
			t.Fatal("file tool link is incomplete")
		}
	})

	t.Run("cache expiry and eviction", func(t *testing.T) {
		now := testBase
		cache := newRemapCache(2, time.Minute)
		cache.now = func() time.Time { return now }
		one, two, three := spanKey{span: testSpan(1)}, spanKey{span: testSpan(2)}, spanKey{span: testSpan(3)}
		cache.put(one, remap{})
		cache.put(two, remap{})
		cache.get(one)
		cache.put(three, remap{})
		if _, ok := cache.get(two); ok {
			t.Fatal("least recently used entry was not evicted")
		}
		now = now.Add(2 * time.Minute)
		if _, ok := cache.get(one); ok {
			t.Fatal("expired entry survived")
		}
	})
}
