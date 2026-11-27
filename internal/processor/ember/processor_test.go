package ember

import (
	"context"
	"reflect"
	"regexp"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/harness/harnesstest"
	"github.com/crucibledx/ember/internal/privacy"
	"github.com/crucibledx/ember/internal/tree"
)

var testBase = time.Unix(1000, 0)

type syntheticAdapter struct {
	harnesstest.Fake
	mapping harness.Mapping
}

func (syntheticAdapter) Name() string               { return "synthetic" }
func (a syntheticAdapter) Mapping() harness.Mapping { return a.mapping }

func syntheticMapping() harness.Mapping {
	return harness.Mapping{
		Resource:   map[string][]string{"service.name": {"native-service"}, "cloud.region": {"test-region"}},
		SessionKey: "session.id", SubagentKey: "agent.id",
		ToolCallKeys: []string{"call.id"}, ToolNameKeys: []string{"tool.name", "gen_ai.tool.name"},
		ToolSpanName: "tool ", RedactedTools: []string{"hidden"},
		MCP:         regexp.MustCompile(`^mcp__(.+?)__(.+)$`),
		NativeAllow: []string{"agent.id", "call.id", "tool.name", "gen_ai.tool.name", "owner.id", "allowed.native"},
		ContentKeys: []string{"content.native"}, OwnerKeys: []string{"owner.id"},
		Metrics: harness.NativeMetrics{Cost: "native.cost", CostUnit: "USD"},
	}
}

func newTestProcessor(mapping harness.Mapping) *emberProcessor {
	if mapping.Resource == nil {
		mapping = syntheticMapping()
	}
	cfg := createDefaultConfig().(*Config)
	cfg.Privacy.HumanIdentity = privacy.HumanPlain
	p := newProcessor(cfg, processortest.NewNopSettings(component.MustNewType("ember")))
	p.registry = registry.New()
	p.adapters = []harness.Adapter{syntheticAdapter{mapping: mapping}}
	return p
}

func testSnapshot(p *emberProcessor, id string) registry.Snapshot {
	root := tree.SessionRootKey("synthetic", id)
	run := harness.Run{Harness: "synthetic", SessionID: id, Kind: harness.KindAgent,
		StartedAt: testBase.Add(-time.Minute), WorkstreamID: "stream-1"}
	snapshot := registry.Snapshot{Run: run, TraceID: tree.TraceID(root), RootKey: root,
		RootSessionID: id, WorkID: "work-1", Idle: time.Minute, LastRecord: testBase.Add(2 * time.Hour),
		Segments:  []tree.Segment{{Number: 1, Start: testBase, End: testBase.Add(2 * time.Hour)}},
		ToolSpans: map[string]registry.SpanRef{}}
	p.registry.Put(snapshot)
	return snapshot
}

func testResource() pcommon.Resource {
	resource := pcommon.NewResource()
	resource.Attributes().PutStr("service.name", "native-service")
	resource.Attributes().PutStr("cloud.region", "test-region")
	return resource
}

func testTraces() (ptrace.Traces, ptrace.ResourceSpans) {
	traces := ptrace.NewTraces()
	resource := traces.ResourceSpans().AppendEmpty()
	testResource().CopyTo(resource.Resource())
	return traces, resource
}

func addSpan(resource ptrace.ResourceSpans, trace pcommon.TraceID, span, parent pcommon.SpanID, name, session string) ptrace.Span {
	var scope ptrace.ScopeSpans
	if resource.ScopeSpans().Len() == 0 {
		scope = resource.ScopeSpans().AppendEmpty()
	} else {
		scope = resource.ScopeSpans().At(0)
	}
	out := scope.Spans().AppendEmpty()
	out.SetTraceID(trace)
	out.SetSpanID(span)
	out.SetParentSpanID(parent)
	out.SetName(name)
	out.SetStartTimestamp(pcommon.NewTimestampFromTime(testBase.Add(time.Minute)))
	out.SetEndTimestamp(pcommon.NewTimestampFromTime(testBase.Add(2 * time.Minute)))
	if session != "" {
		out.Attributes().PutStr("session.id", session)
	}
	return out
}

func testTrace(id byte) (out pcommon.TraceID) { out[15] = id; return }
func testSpan(id byte) (out pcommon.SpanID)   { out[7] = id; return }

func processTestTraces(t *testing.T, p *emberProcessor, traces ptrace.Traces) {
	t.Helper()
	if err := p.processTraces(context.Background(), traces); err != nil {
		t.Fatal(err)
	}
}

func stringAttr(attrs pcommon.Map, key string) string {
	value, _ := attrs.Get(key)
	return value.Str()
}

func registryCounter(r *registry.Registry, name string) uint64 {
	return reflect.ValueOf(r).Elem().FieldByName(name).Uint()
}
