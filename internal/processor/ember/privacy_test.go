package ember

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/privacy"
	"github.com/crucibledx/ember/internal/tree"
)

func TestPrivacy(t *testing.T) {
	const secret = "planted-secret-value"
	t.Run("independent §8.1 contract and emitted keys", testEmberKeyContract)
	t.Run("every location defaults and deny precedence", func(t *testing.T) {
		mapping := syntheticMapping()
		mapping.NativeAllow = append(mapping.NativeAllow, "denied.native")
		p := newTestProcessor(mapping)
		endpoint := startRegistry(t, p)
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
		body, status := getStatus(t, endpoint)
		if strings.Contains(string(body), secret) {
			t.Fatal("GET /status leaked a dropped value")
		}
		found := false
		for _, dropped := range status.Dropped {
			if dropped.Key == "private.resource" && dropped.Count == 1 {
				found = true
			}
		}
		if !found {
			t.Fatalf("GET /status dropped = %v", status.Dropped)
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
			mapping := syntheticMapping()
			mapping.OwnerKeys = append(mapping.OwnerKeys, "user.id", "user.account_uuid")
			mapping.NativeAllow = append(mapping.NativeAllow, "user.id", "user.account_uuid")
			p := newTestProcessor(mapping)
			p.config.Privacy.HumanIdentity = identity
			p.config.Privacy.HashKey = "test-key"
			testSnapshot(p, "run")
			traces, resource := testTraces()
			span := addSpan(resource, testTrace(3), testSpan(1), pcommon.SpanID{}, "root", "run")
			span.Attributes().PutStr("owner.id", "native-owner")
			span.Attributes().PutStr("user.id", "native-owner")
			span.Attributes().PutStr("user.account_uuid", "native-owner")
			span.Attributes().PutStr("ember.human.active", "h1:already,native-owner")
			span.Attributes().PutStr("user.email", secret)
			numeric := addSpan(resource, testTrace(3), testSpan(2), testSpan(1), "numeric", "run")
			numeric.Attributes().PutInt("user.id", 42)
			numeric.Attributes().PutInt("user.account_uuid", 43)
			processTestTraces(t, p, traces)
			started := stringAttr(span.Attributes(), "ember.human.started_by")
			owner := stringAttr(span.Attributes(), "owner.id")
			switch identity {
			case privacy.HumanPlain:
				if started != "synthetic:native-owner" || owner != "native-owner" || stringAttr(span.Attributes(), "user.id") != "native-owner" {
					t.Fatalf("plain humans = %q/%q", started, owner)
				}
			case privacy.HumanHash:
				if !strings.HasPrefix(started, "h1:") || !strings.HasPrefix(owner, "h1:") || strings.Contains(started, "native-owner") ||
					stringAttr(span.Attributes(), "user.id") != owner || stringAttr(span.Attributes(), "user.account_uuid") != owner {
					t.Fatalf("hashed humans = %q/%q", started, owner)
				}
				if active := stringAttr(span.Attributes(), "ember.human.active"); !strings.HasPrefix(active, "h1:already,h1:") {
					t.Fatalf("active humans = %q", active)
				}
				if _, ok := numeric.Attributes().Get("user.id"); ok {
					t.Fatal("integer user.id survived hash mode")
				}
				if _, ok := numeric.Attributes().Get("user.account_uuid"); ok {
					t.Fatal("integer user.account_uuid survived hash mode")
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

const independentEmberKeys = `
service.name service.version service.instance.id host.name os.type host.arch ember.hash_key.id
session.id gen_ai.operation.name gen_ai.conversation.id gen_ai.agent.name gen_ai.request.model
ember.harness ember.agent.kind ember.workstream.id ember.work.id ember.parent.session.id ember.parent.tool_call.id
ember.human.started_by ember.human.active ember.session.segment ember.agent.llm_turns ember.agent.tool_calls
gen_ai.tool.name gen_ai.tool.call.id error.type ember.tool.incomplete ember.mcp.server ember.mcp.tool ember.spawned.session_ids
event.name ember.agent.status ember.tool.duration_ms ember.llm.turn ember.root.session.id ember.agent.children
gen_ai.token.type ember.original_trace_id ember.link.type`

func testEmberKeyContract(t *testing.T) {
	expected := map[string]bool{}
	attrs := pcommon.NewMap()
	for _, key := range strings.Fields(independentEmberKeys) {
		expected[key] = true
		attrs.PutStr(key, "value")
	}
	processor := newTestProcessor(harness.Mapping{})
	policy, err := processor.policy(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if dropped := policy.Filter(attrs); len(dropped) != 0 || attrs.Len() != len(expected) {
		t.Fatalf("independent §8.1 keys dropped: %v", dropped)
	}
	check := func(attrs pcommon.Map) {
		for key := range attrs.All() {
			if !expected[key] {
				t.Errorf("emitted key %q is absent from independent §8.1 contract", key)
			}
		}
	}
	run := harness.Run{Harness: "synthetic", SessionID: "run", Kind: harness.KindAgent, AgentType: "agent",
		Status: harness.StatusCompleted, StartedAt: testBase, EndedAt: testBase.Add(10 * time.Minute), Model: "model",
		TurnTimes: []time.Time{testBase.Add(time.Minute)}, Usage: map[string]harness.Usage{"model": {Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Credits: 5}},
		ToolCalls: []harness.ToolCall{{ID: "call", Name: "tool", StartedAt: testBase.Add(2 * time.Minute), EndedAt: testBase.Add(3 * time.Minute), SpawnedSessionIDs: []string{"child"}}}}
	node := &tree.Node{Run: run, Parent: &harness.ParentRef{SessionID: "parent", ToolCallID: "parent-call"},
		RootKey: tree.SessionRootKey("synthetic", "run"), RootSessionID: "run", WorkstreamID: "stream", WorkID: "work",
		StartedBy: "human", Active: []string{"human"}, Idle: time.Minute,
		Segments: []tree.Segment{{Number: 1, Start: testBase, End: testBase.Add(10 * time.Minute), Closed: true, LLMTurns: 1, ToolCalls: 1}}}
	forest := &tree.Forest{Nodes: map[string]*tree.Node{"run": node}}
	config := emit.Config{Resource: emit.Resource{Version: "1", HostName: "host", OSType: "os", HostArch: "arch", HashKeyID: "key"}}
	traces := emit.Traces(forest, config)
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		resource := traces.ResourceSpans().At(i)
		check(resource.Resource().Attributes())
		for j := 0; j < resource.ScopeSpans().Len(); j++ {
			spans := resource.ScopeSpans().At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				check(spans.At(k).Attributes())
			}
		}
	}
	logs := emit.Logs(forest, config)
	for i := 0; i < logs.ResourceLogs().Len(); i++ {
		resource := logs.ResourceLogs().At(i)
		check(resource.Resource().Attributes())
		for j := 0; j < resource.ScopeLogs().Len(); j++ {
			records := resource.ScopeLogs().At(j).LogRecords()
			for k := 0; k < records.Len(); k++ {
				check(records.At(k).Attributes())
			}
		}
	}
	metrics := emit.Metrics(forest, config, testBase.Add(time.Hour))
	for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
		resource := metrics.ResourceMetrics().At(i)
		check(resource.Resource().Attributes())
		for j := 0; j < resource.ScopeMetrics().Len(); j++ {
			for k := 0; k < resource.ScopeMetrics().At(j).Metrics().Len(); k++ {
				visitPoints(resource.ScopeMetrics().At(j).Metrics().At(k), func(attrs pcommon.Map, _ time.Time) { check(attrs) })
			}
		}
	}

	snapshot := testSnapshot(processor, "run")
	snapshot.Run.ToolCalls = run.ToolCalls
	processor.registry.Put(snapshot)
	native, resource := testTraces()
	span := addSpan(resource, testTrace(8), testSpan(1), pcommon.SpanID{}, "tool native", "run")
	span.Attributes().PutStr("call.id", "call")
	span.Attributes().PutStr("tool.name", "mcp__server__tool")
	processTestTraces(t, processor, native)
	for key := range span.Attributes().All() {
		if strings.HasPrefix(key, "ember.") && !expected[key] {
			t.Errorf("processor key %q is absent from independent §8.1 contract", key)
		}
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
