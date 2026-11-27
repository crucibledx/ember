package ember

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
)

func TestNativeResourceWorkIDAuthority(t *testing.T) {
	key := registry.Key{Harness: "synthetic", RunID: "run"}

	t.Run("span updates registry and overrides later file snapshots", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		testSnapshot(p, "run")
		traces, resource := testTraces()
		resource.Resource().Attributes().PutStr("ember.work.id", "ISSUE-123")
		span := addSpan(resource, testTrace(1), testSpan(1), pcommon.SpanID{}, "invoke_agent", "run")
		processTestTraces(t, p, traces)

		if got := stringAttr(span.Attributes(), "ember.work.id"); got != "ISSUE-123" {
			t.Fatalf("native span work id = %q", got)
		}
		if got, ok := p.registry.WorkID(key); !ok || got != "ISSUE-123" {
			t.Fatalf("registry native work id = %q, %v", got, ok)
		}
		stored, _ := p.registry.Get(key)
		stored.WorkID = "file-fallback"
		p.registry.Put(stored)
		stored, _ = p.registry.Get(key)
		if stored.WorkID != "ISSUE-123" {
			t.Fatalf("file snapshot replaced native work id with %q", stored.WorkID)
		}
	})

	t.Run("log records native resource id", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		testSnapshot(p, "run")
		logs := plog.NewLogs()
		resource := logs.ResourceLogs().AppendEmpty()
		testResource().CopyTo(resource.Resource())
		resource.Resource().Attributes().PutStr("ember.work.id", "ISSUE-123")
		record := resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.Attributes().PutStr("session.id", "run")
		if err := p.processLogs(context.Background(), logs); err != nil {
			t.Fatal(err)
		}
		if got := stringAttr(record.Attributes(), "ember.work.id"); got != "ISSUE-123" {
			t.Fatalf("native log work id = %q", got)
		}
		if got, ok := p.registry.WorkID(key); !ok || got != "ISSUE-123" {
			t.Fatalf("registry native work id = %q, %v", got, ok)
		}
	})

	t.Run("metric points record native resource id", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		testSnapshot(p, "run")
		metrics := pmetric.NewMetrics()
		resource := metrics.ResourceMetrics().AppendEmpty()
		testResource().CopyTo(resource.Resource())
		resource.Resource().Attributes().PutStr("ember.work.id", "ISSUE-123")
		point := resource.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetEmptyGauge().DataPoints().AppendEmpty()
		point.Attributes().PutStr("session.id", "run")
		if err := p.processMetrics(context.Background(), metrics); err != nil {
			t.Fatal(err)
		}
		if got := stringAttr(point.Attributes(), "ember.work.id"); got != "ISSUE-123" {
			t.Fatalf("native metric work id = %q", got)
		}
		if got, ok := p.registry.WorkID(key); !ok || got != "ISSUE-123" {
			t.Fatalf("registry native work id = %q, %v", got, ok)
		}
	})

	t.Run("unresolved log records native work id for later discovery", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		logs := plog.NewLogs()
		resource := logs.ResourceLogs().AppendEmpty()
		testResource().CopyTo(resource.Resource())
		resource.Resource().Attributes().PutStr("ember.work.id", "ISSUE-123")
		record := resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.Attributes().PutStr("session.id", "run")
		if err := p.processLogs(context.Background(), logs); err != nil {
			t.Fatal(err)
		}
		if got, ok := p.registry.WorkID(key); !ok || got != "ISSUE-123" {
			t.Fatalf("unresolved log native work id = %q, %v", got, ok)
		}
		if _, ok := record.Attributes().Get("ember.work.id"); ok {
			t.Fatal("unresolved log was enriched before run discovery")
		}
	})

	t.Run("unresolved metric records native work id for later discovery", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		metrics := pmetric.NewMetrics()
		resource := metrics.ResourceMetrics().AppendEmpty()
		testResource().CopyTo(resource.Resource())
		resource.Resource().Attributes().PutStr("ember.work.id", "ISSUE-123")
		point := resource.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetEmptyGauge().DataPoints().AppendEmpty()
		point.Attributes().PutStr("session.id", "run")
		if err := p.processMetrics(context.Background(), metrics); err != nil {
			t.Fatal(err)
		}
		if got, ok := p.registry.WorkID(key); !ok || got != "ISSUE-123" {
			t.Fatalf("unresolved metric native work id = %q, %v", got, ok)
		}
		if _, ok := point.Attributes().Get("ember.work.id"); ok {
			t.Fatal("unresolved metric was enriched before run discovery")
		}
	})
	t.Run("file fallback is not native authority", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		testSnapshot(p, "run")
		if _, ok := p.registry.WorkID(key); ok {
			t.Fatal("file snapshot work id was promoted to native authority")
		}
	})
}
