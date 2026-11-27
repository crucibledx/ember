package ember

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/extensiontest"
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
		RootSessionID: id, WorkstreamID: "stream-1", WorkID: "work-1", Idle: time.Minute, LastRecord: testBase.Add(2 * time.Hour),
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

type registryHost struct {
	extensions map[component.ID]component.Component
}

func (h registryHost) GetExtensions() map[component.ID]component.Component       { return h.extensions }
func (registryHost) GetFactory(component.Kind, component.Type) component.Factory { return nil }

func startRegistry(t *testing.T, p *emberProcessor) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	factory := registry.NewFactory()
	cfg := factory.CreateDefaultConfig().(*registry.Config)
	cfg.Endpoint = endpoint
	extension, err := factory.Create(context.Background(), extensiontest.NewNopSettings(factory.Type()), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := extension.Start(context.Background(), registryHost{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := extension.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	p.registry = nil
	host := registryHost{extensions: map[component.ID]component.Component{component.MustNewID("ember"): extension}}
	if err := p.Start(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	return endpoint
}

type statusResponse struct {
	Dropped []struct {
		Key   string `json:"key"`
		Count uint64 `json:"count"`
	} `json:"dropped"`
}

func getStatus(t *testing.T, endpoint string) ([]byte, statusResponse) {
	t.Helper()
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + endpoint + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /status status = %s", response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var status statusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	return body, status
}
