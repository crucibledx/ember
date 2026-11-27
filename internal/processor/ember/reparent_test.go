package ember

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/crucibledx/ember/internal/emit"
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

	t.Run("segment discovery", func(t *testing.T) {
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

	})

	t.Run("newer children reparse without changing inbound placement", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		snapshot := testSnapshot(p, "run")
		snapshot.LastRecord = testBase.Add(2 * time.Minute)
		snapshot.WorkID = "stale"
		p.registry.Put(snapshot)
		fresh := snapshot
		fresh.TraceID = tree.TraceID("fresh-but-not-inbound-root")
		calls := 0
		p.resolveFn = func(_ context.Context, _ registry.Key, _ time.Time) (registry.Snapshot, error) {
			calls++
			p.registry.Put(fresh)
			return fresh, nil
		}
		traces, resource := testTraces()
		root := addSpan(resource, testTrace(7), testSpan(1), pcommon.SpanID{}, "root", "run")
		root.SetStartTimestamp(pcommon.NewTimestampFromTime(testBase.Add(time.Minute)))
		child := addSpan(resource, testTrace(7), testSpan(2), testSpan(1), "child", "")
		child.SetStartTimestamp(pcommon.NewTimestampFromTime(testBase.Add(3 * time.Minute)))
		fresh.WorkID = "fresh-in-batch"
		processTestTraces(t, p, traces)
		if calls != 1 || child.TraceID() != root.TraceID() || stringAttr(child.Attributes(), "ember.work.id") != "fresh-in-batch" {
			t.Fatalf("in-batch reparse calls=%d work=%q", calls, stringAttr(child.Attributes(), "ember.work.id"))
		}

		stale := snapshot
		p.registry.Put(stale)
		fresh.WorkID = "fresh-later-cache"
		later, laterResource := testTraces()
		laterChild := addSpan(laterResource, testTrace(7), testSpan(3), testSpan(1), "later", "")
		laterChild.SetStartTimestamp(pcommon.NewTimestampFromTime(testBase.Add(4 * time.Minute)))
		processTestTraces(t, p, later)
		if calls != 2 || laterChild.TraceID() != root.TraceID() || laterChild.ParentSpanID() != root.SpanID() ||
			stringAttr(laterChild.Attributes(), "ember.work.id") != "fresh-later-cache" {
			t.Fatalf("later-cache reparse calls=%d trace=%s parent=%s", calls, laterChild.TraceID(), laterChild.ParentSpanID())
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

		other, otherResource := testTraces()
		otherResource.Resource().Attributes().PutStr("service.name", "other-service")
		otherSpan := addSpan(otherResource, testTrace(44), testSpan(4), testSpan(9), "other", "unmatched")
		processTestTraces(t, p, other)
		if otherSpan.TraceID() != testTrace(44) || otherSpan.SpanID() != testSpan(4) || otherSpan.ParentSpanID() != testSpan(9) ||
			registryCounter(p.registry, "unresolved") != 1 {
			t.Fatal("unmatched service changed structure or unresolved count")
		}

		fallbackProcessor := newTestProcessor(harness.Mapping{})
		fallback := testSnapshot(fallbackProcessor, "parent")
		fallbackTraces, fallbackResource := testTraces()
		fallbackSpan := addSpan(fallbackResource, testTrace(45), testSpan(1), pcommon.SpanID{}, "fallback", "parent")
		fallbackSpan.Attributes().PutStr("agent.id", "missing")
		processTestTraces(t, fallbackProcessor, fallbackTraces)
		wantFallback, _ := decodeTrace(fallback.TraceID)
		if fallbackSpan.TraceID() != wantFallback || registryCounter(fallbackProcessor.registry, "unresolved") != 0 {
			t.Fatal("failed composite run did not fall back to its base session")
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

		parent := testSnapshot(p, "parent")
		parent.Run.WorkstreamID = ""
		parent.Run.Status = harness.StatusCompleted
		parent.Run.EndedAt = testBase.Add(time.Minute)
		parent.Run.ToolCalls = []harness.ToolCall{{ID: "spawn", Name: "task"}}
		p.registry.Put(parent)
		childSnapshot := testSnapshot(p, "child")
		childSnapshot.Run.WorkstreamID = ""
		childSnapshot.Run.Status = harness.StatusCompleted
		childSnapshot.Run.EndedAt = testBase.Add(time.Minute)
		p.registry.Put(childSnapshot)
		first, firstResource := testTraces()
		parentSpan := addSpan(firstResource, testTrace(5), testSpan(1), pcommon.SpanID{}, "tool parent", "parent")
		parentSpan.Attributes().PutStr("call.id", "spawn")
		processTestTraces(t, p, first)
		second, secondResource := testTraces()
		child := addSpan(secondResource, testTrace(5), testSpan(2), testSpan(1), "child", "child")
		processTestTraces(t, p, second)
		toolID, _ := decodeSpan(tree.ToolSpanID("synthetic", "spawn"))
		if child.TraceID() != parentSpan.TraceID() || child.ParentSpanID() != toolID {
			t.Fatal("child did not follow moved inbound tool parent")
		}
		parentKey, call, ok := p.registry.Parent(registry.Key{Harness: "synthetic", RunID: "child"})
		if !ok {
			t.Fatal("moved inbound parent was not recorded")
		}
		childSnapshot.Run.Parent = &harness.ParentRef{SessionID: parentKey.RunID, ToolCallID: call}
		forest := tree.BuildForest([]harness.Run{parent.Run, childSnapshot.Run}, tree.Options{
			Now: testBase.Add(time.Hour), Idle: time.Minute, Roots: &tree.RootPins{},
		})
		fileTraces := emit.Traces(forest, emit.Config{})
		found := false
		for i := 0; i < fileTraces.ResourceSpans().Len(); i++ {
			spans := fileTraces.ResourceSpans().At(i).ScopeSpans().At(0).Spans()
			for j := 0; j < spans.Len(); j++ {
				segment := spans.At(j)
				number, _ := segment.Attributes().Get("ember.session.segment")
				if stringAttr(segment.Attributes(), "session.id") == "child" && number.Int() == 1 {
					found = true
					if segment.TraceID() != parentSpan.TraceID() || segment.ParentSpanID() != toolID {
						t.Fatalf("file child placement = %s/%s want %s/%s parent=%v", segment.TraceID(), segment.ParentSpanID(), parentSpan.TraceID(), toolID, childSnapshot.Run.Parent)
					}
				}
			}
		}
		if !found {
			t.Fatal("file-built child segment was not emitted")
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
