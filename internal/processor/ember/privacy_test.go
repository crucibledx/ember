package ember

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/privacy"
)

func TestPrivacy(t *testing.T) {
	const secret = "planted-secret-value"
	t.Run("every location defaults and deny precedence", func(t *testing.T) {
		mapping := syntheticMapping()
		mapping.NativeAllow = append(mapping.NativeAllow, "denied.native")
		p := newTestProcessor(mapping)
		p.config.Privacy.Allow = []string{"global.allowed", "denied.native", "user.email"}
		p.config.Privacy.Deny = []string{"denied.native"}
		testSnapshot(p, "run")
		traces, resource := testTraces()
		resource.Resource().Attributes().PutStr("private.resource", secret)
		resource.Resource().Attributes().PutStr("user.email", secret)
		resource.Resource().Attributes().PutStr("global.allowed", "safe")
		resource.Resource().Attributes().PutStr("denied.native", secret)
		scope := resource.ScopeSpans().AppendEmpty()
		scope.Scope().Attributes().PutStr("private.scope", secret)
		span := addSpan(resource, testTrace(1), testSpan(1), pcommon.SpanID{}, "root", "run")
		span.Attributes().PutStr("private.span", secret)
		span.Attributes().PutStr("content.native", secret)
		span.Status().SetMessage(secret)
		event := span.Events().AppendEmpty()
		event.Attributes().PutStr("private.event", secret)
		link := span.Links().AppendEmpty()
		link.Attributes().PutStr("private.link", secret)
		processTestTraces(t, p, traces)
		if stringAttr(resource.Resource().Attributes(), "global.allowed") != "safe" {
			t.Fatal("global allow was not applied")
		}
		for _, attrs := range []pcommon.Map{resource.Resource().Attributes(), scope.Scope().Attributes(), span.Attributes(), event.Attributes(), link.Attributes()} {
			assertNoSecret(t, attrs, secret)
		}
		if _, ok := resource.Resource().Attributes().Get("denied.native"); ok {
			t.Fatal("deny did not beat native and global allow")
		}
		if span.Status().Message() != "" {
			t.Fatal("status message survived content-off privacy")
		}

		logs := plog.NewLogs()
		logResource := logs.ResourceLogs().AppendEmpty()
		testResource().CopyTo(logResource.Resource())
		logScope := logResource.ScopeLogs().AppendEmpty()
		logScope.Scope().Attributes().PutStr("private.log.scope", secret)
		record := logScope.LogRecords().AppendEmpty()
		record.Attributes().PutStr("session.id", "run")
		record.Attributes().PutStr("event.name", "safe-event")
		record.Attributes().PutStr("private.log", secret)
		record.Body().SetStr(secret)
		if err := p.processLogs(context.Background(), logs); err != nil {
			t.Fatal(err)
		}
		assertNoSecret(t, record.Attributes(), secret)
		assertNoSecret(t, logScope.Scope().Attributes(), secret)
		if record.Body().Str() != "safe-event" {
			t.Fatalf("body = %q", record.Body().Str())
		}

		metrics := privateMetrics(secret)
		if err := p.processMetrics(context.Background(), metrics); err != nil {
			t.Fatal(err)
		}
		scopeMetrics := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0)
		for i := 0; i < scopeMetrics.Metrics().Len(); i++ {
			visitPoints(scopeMetrics.Metrics().At(i), func(attrs pcommon.Map, _ time.Time) { assertNoSecret(t, attrs, secret) })
		}
		dropped := reflect.ValueOf(p.registry).Elem().FieldByName("dropped")
		if dropped.MapIndex(reflect.ValueOf("private.resource")).IsZero() {
			t.Fatal("dropped key was not counted")
		}
		for _, key := range dropped.MapKeys() {
			if strings.Contains(key.String(), secret) {
				t.Fatalf("drop report contains a value: %s", key.String())
			}
		}
	})

	t.Run("content on passes only content keys and bodies", func(t *testing.T) {
		p := newTestProcessor(harness.Mapping{})
		p.config.Privacy.Content = true
		testSnapshot(p, "run")
		traces, resource := testTraces()
		span := addSpan(resource, testTrace(2), testSpan(1), pcommon.SpanID{}, "root", "run")
		span.Attributes().PutStr("content.native", secret)
		span.Attributes().PutStr("private.span", secret)
		span.Status().SetMessage(secret)
		processTestTraces(t, p, traces)
		if stringAttr(span.Attributes(), "content.native") != secret || span.Status().Message() != secret {
			t.Fatal("content-enabled data was removed")
		}
		if _, ok := span.Attributes().Get("private.span"); ok {
			t.Fatal("content mode allowed an arbitrary key")
		}
		logs := plog.NewLogs()
		logResource := logs.ResourceLogs().AppendEmpty()
		testResource().CopyTo(logResource.Resource())
		record := logResource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.Attributes().PutStr("session.id", "run")
		record.Body().SetStr(secret)
		if err := p.processLogs(context.Background(), logs); err != nil {
			t.Fatal(err)
		}
		if record.Body().Str() != secret {
			t.Fatal("content-enabled body was replaced")
		}
	})

	for _, identity := range []privacy.HumanIdentity{privacy.HumanPlain, privacy.HumanHash, privacy.HumanOff} {
		t.Run("human "+string(identity), func(t *testing.T) {
			p := newTestProcessor(harness.Mapping{})
			p.config.Privacy.HumanIdentity = identity
			p.config.Privacy.HashKey = "test-key"
			testSnapshot(p, "run")
			traces, resource := testTraces()
			span := addSpan(resource, testTrace(3), testSpan(1), pcommon.SpanID{}, "root", "run")
			span.Attributes().PutStr("owner.id", "native-owner")
			span.Attributes().PutStr("ember.human.active", "h1:already,native-owner")
			span.Attributes().PutStr("user.email", secret)
			processTestTraces(t, p, traces)
			started := stringAttr(span.Attributes(), "ember.human.started_by")
			owner := stringAttr(span.Attributes(), "owner.id")
			switch identity {
			case privacy.HumanPlain:
				if started != "synthetic:native-owner" || owner != "native-owner" {
					t.Fatalf("plain humans = %q/%q", started, owner)
				}
			case privacy.HumanHash:
				if !strings.HasPrefix(started, "h1:") || !strings.HasPrefix(owner, "h1:") || strings.Contains(started, "native-owner") {
					t.Fatalf("hashed humans = %q/%q", started, owner)
				}
				if active := stringAttr(span.Attributes(), "ember.human.active"); !strings.HasPrefix(active, "h1:already,h1:") {
					t.Fatalf("active humans = %q", active)
				}
			case privacy.HumanOff:
				if started != "" || owner != "" || stringAttr(span.Attributes(), "ember.human.active") != "" {
					t.Fatal("human-off exported an identifier")
				}
			}
			if _, ok := span.Attributes().Get("user.email"); ok {
				t.Fatal("user.email survived")
			}
		})
	}
}

func assertNoSecret(t *testing.T, attrs pcommon.Map, secret string) {
	t.Helper()
	for key, value := range attrs.All() {
		if strings.Contains(value.AsString(), secret) {
			t.Errorf("secret survived at %s", key)
		}
	}
}

func privateMetrics(secret string) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resource := metrics.ResourceMetrics().AppendEmpty()
	testResource().CopyTo(resource.Resource())
	resource.Resource().Attributes().PutStr("private.metric.resource", secret)
	scope := resource.ScopeMetrics().AppendEmpty()
	scope.Scope().Attributes().PutStr("private.metric.scope", secret)
	gauge := scope.Metrics().AppendEmpty().SetEmptyGauge().DataPoints().AppendEmpty()
	sum := scope.Metrics().AppendEmpty().SetEmptySum().DataPoints().AppendEmpty()
	histogram := scope.Metrics().AppendEmpty().SetEmptyHistogram().DataPoints().AppendEmpty()
	exponential := scope.Metrics().AppendEmpty().SetEmptyExponentialHistogram().DataPoints().AppendEmpty()
	summary := scope.Metrics().AppendEmpty().SetEmptySummary().DataPoints().AppendEmpty()
	for _, attrs := range []pcommon.Map{gauge.Attributes(), sum.Attributes(), histogram.Attributes(), exponential.Attributes(), summary.Attributes()} {
		attrs.PutStr("session.id", "run")
		attrs.PutStr("private.point", secret)
	}
	return metrics
}
