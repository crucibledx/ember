package ember

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

func TestReparentChildFirstPrefersParentRun(t *testing.T) {
	p := newTestProcessor(harness.Mapping{})
	base := testSnapshot(p, "parent")
	composite := testSnapshot(p, "parent/child")
	traces, resource := testTraces()
	child := addSpan(resource, testTrace(31), testSpan(2), testSpan(1), "child", "parent")
	parent := addSpan(resource, testTrace(31), testSpan(1), pcommon.SpanID{}, "parent", "parent")
	parent.Attributes().PutStr("agent.id", "child")

	processTestTraces(t, p, traces)

	wantTrace, _ := decodeTrace(composite.TraceID)
	if child.TraceID() != wantTrace || child.ParentSpanID() != parent.SpanID() {
		t.Fatalf("child placement = %s/%s, want composite trace %s under parent %s", child.TraceID(), child.ParentSpanID(), wantTrace, parent.SpanID())
	}
	baseKey := registry.Key{Harness: "synthetic", RunID: base.Run.SessionID}
	if parentKey, call, ok := p.registry.Parent(baseKey); ok {
		t.Fatalf("base session recorded as its own child: parent=%v call=%q", parentKey, call)
	}
}

func TestReparentBaseFallbackPrecedesGroupRoot(t *testing.T) {
	p := newTestProcessor(harness.Mapping{})
	parentSnapshot := testSnapshot(p, "parent")
	testSnapshot(p, "other")
	traces, resource := testTraces()
	grandchild := addSpan(resource, testTrace(32), testSpan(3), testSpan(1), "grandchild", "")
	addSpan(resource, testTrace(32), testSpan(2), pcommon.SpanID{}, "other", "other")
	parent := addSpan(resource, testTrace(32), testSpan(1), pcommon.SpanID{}, "parent", "parent")
	parent.Attributes().PutStr("agent.id", "child")

	processTestTraces(t, p, traces)

	wantTrace, _ := decodeTrace(parentSnapshot.TraceID)
	if grandchild.TraceID() != wantTrace || grandchild.ParentSpanID() != parent.SpanID() {
		t.Fatalf("grandchild placement = %s/%s, want base parent %s under %s", grandchild.TraceID(), grandchild.ParentSpanID(), wantTrace, parent.SpanID())
	}
}
