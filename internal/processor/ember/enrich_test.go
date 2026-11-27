package ember

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

func TestEnrich(t *testing.T) {
	t.Run("resource matching", func(t *testing.T) {
		resource := testResource()
		if _, ok := matchResource([]harness.Adapter{syntheticAdapter{mapping: syntheticMapping()}}, resource); !ok {
			t.Fatal("all mapping keys and accepted values did not match")
		}
		resource.Attributes().PutStr("cloud.region", "other")
		if _, ok := matchResource([]harness.Adapter{syntheticAdapter{mapping: syntheticMapping()}}, resource); ok {
			t.Fatal("one mismatched resource key was accepted")
		}
		if _, ok := matchResource([]harness.Adapter{syntheticAdapter{mapping: harness.Mapping{}}}, testResource()); ok {
			t.Fatal("empty mapping matched a resource")
		}
	})

	t.Run("span owner and spawned sessions", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		snapshot := testSnapshot(p, "run")
		snapshot.Run.ToolCalls = []harness.ToolCall{{ID: "call", Name: "task", SpawnedSessionIDs: []string{"child-b", "child-a"}}}
		p.registry.Put(snapshot)
		traces, resource := testTraces()
		span := addSpan(resource, testTrace(1), testSpan(1), pcommon.SpanID{}, "tool spawn", "run")
		span.Attributes().PutStr("call.id", "call")
		span.Attributes().PutStr("owner.id", "native-owner")
		child := addSpan(resource, testTrace(1), testSpan(2), testSpan(1), "execution", "run")
		child.Attributes().PutStr("call.id", "call")
		processTestTraces(t, p, traces)
		if _, ok := child.Attributes().Get("ember.spawned.session_ids"); ok {
			t.Fatal("non-tool execution span gained spawned sessions")
		}
		for key, want := range map[string]string{
			"ember.harness": "synthetic", "ember.agent.kind": "agent", "ember.workstream.id": "stream-1",
			"ember.work.id": "work-1", "ember.human.started_by": "synthetic:native-owner",
			"ember.spawned.session_ids": "child-b,child-a",
		} {
			if got := stringAttr(span.Attributes(), key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		stored, _ := p.registry.Get(registry.Key{Harness: "synthetic", RunID: "run"})
		if stored.Run.Owner != "native-owner" {
			t.Fatalf("registry owner = %q", stored.Run.Owner)
		}
	})

	t.Run("logs receive only missing ids", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		snapshot := testSnapshot(p, "run")
		snapshot.Segments = []tree.Segment{
			{Number: 1, Start: testBase, End: testBase.Add(time.Minute)},
			{Number: 2, Start: testBase.Add(10 * time.Minute), End: testBase.Add(20 * time.Minute)},
		}
		p.registry.Put(snapshot)
		logs := plog.NewLogs()
		resource := logs.ResourceLogs().AppendEmpty()
		testResource().CopyTo(resource.Resource())
		records := resource.ScopeLogs().AppendEmpty().LogRecords()
		tool := records.AppendEmpty()
		tool.Attributes().PutStr("session.id", "run")
		tool.Attributes().PutStr("call.id", "call")
		tool.SetTimestamp(pcommon.NewTimestampFromTime(testBase.Add(11 * time.Minute)))
		segment := records.AppendEmpty()
		segment.Attributes().PutStr("session.id", "run")
		segment.SetTimestamp(pcommon.NewTimestampFromTime(testBase.Add(11 * time.Minute)))
		existing := records.AppendEmpty()
		existing.Attributes().PutStr("session.id", "run")
		existing.SetTraceID(testTrace(88))
		existing.SetSpanID(testSpan(77))
		if err := p.processLogs(context.Background(), logs); err != nil {
			t.Fatal(err)
		}
		wantTrace, _ := decodeTrace(snapshot.TraceID)
		wantTool, _ := decodeSpan(tree.ToolSpanID("synthetic", "call"))
		wantSegment, _ := decodeSpan(tree.SegmentSpanID("synthetic", "run", 2))
		if tool.TraceID() != wantTrace || tool.SpanID() != wantTool {
			t.Fatalf("tool log ids = %s/%s", tool.TraceID(), tool.SpanID())
		}
		if segment.TraceID() != wantTrace || segment.SpanID() != wantSegment {
			t.Fatalf("segment log ids = %s/%s want %s", segment.TraceID(), segment.SpanID(), wantSegment)
		}
		if existing.TraceID() != testTrace(88) || existing.SpanID() != testSpan(77) {
			t.Fatal("existing log ids changed")
		}
		if stringAttr(segment.Attributes(), "ember.work.id") != "work-1" {
			t.Fatal("log was not enriched")
		}
	})

	t.Run("every metric point and native cost", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		testSnapshot(p, "run")
		metrics := pmetric.NewMetrics()
		resource := metrics.ResourceMetrics().AppendEmpty()
		testResource().CopyTo(resource.Resource())
		scope := resource.ScopeMetrics().AppendEmpty()
		gauge := scope.Metrics().AppendEmpty()
		gauge.SetName("native.cost")
		point := gauge.SetEmptyGauge().DataPoints().AppendEmpty()
		point.SetDoubleValue(12.5)
		setPoint(point.Attributes())
		sum := scope.Metrics().AppendEmpty().SetEmptySum().DataPoints().AppendEmpty()
		setPoint(sum.Attributes())
		histogram := scope.Metrics().AppendEmpty().SetEmptyHistogram().DataPoints().AppendEmpty()
		setPoint(histogram.Attributes())
		exponential := scope.Metrics().AppendEmpty().SetEmptyExponentialHistogram().DataPoints().AppendEmpty()
		setPoint(exponential.Attributes())
		summary := scope.Metrics().AppendEmpty().SetEmptySummary().DataPoints().AppendEmpty()
		setPoint(summary.Attributes())
		if err := p.processMetrics(context.Background(), metrics); err != nil {
			t.Fatal(err)
		}
		points := 0
		for i := 0; i < scope.Metrics().Len(); i++ {
			visitPoints(scope.Metrics().At(i), func(attrs pcommon.Map, _ time.Time) {
				points++
				if stringAttr(attrs, "ember.harness") != "synthetic" || stringAttr(attrs, "ember.agent.kind") != "agent" ||
					stringAttr(attrs, "ember.workstream.id") != "stream-1" || stringAttr(attrs, "ember.work.id") != "work-1" ||
					stringAttr(attrs, "ember.human.started_by") != "synthetic:native-owner" {
					t.Errorf("point attributes = %v", attrs.AsRaw())
				}
			})
		}
		if points != 5 || point.DoubleValue() != 12.5 || gauge.Unit() != "" {
			t.Fatalf("points=%d cost=%v unit=%q", points, point.DoubleValue(), gauge.Unit())
		}
	})
}

func setPoint(attrs pcommon.Map) {
	attrs.PutStr("session.id", "run")
	attrs.PutStr("owner.id", "native-owner")
}
