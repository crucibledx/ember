package sessions

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/crucibledx/ember/internal/extension/registry"
	"github.com/crucibledx/ember/internal/harness"
	processor "github.com/crucibledx/ember/internal/processor/ember"
)

type nativeWorkIDAdapter struct {
	name         string
	runs         map[string]harness.Run
	includeChild bool
	discoverable bool
}

func (a *nativeWorkIDAdapter) Name() string                                             { return a.name }
func (a *nativeWorkIDAdapter) Detect(harness.Env) harness.Detection                     { return harness.Detection{} }
func (a *nativeWorkIDAdapter) Setup(harness.Env, harness.SetupOptions) []harness.Change { return nil }
func (a *nativeWorkIDAdapter) Sessions(harness.Env) ([]harness.SessionRef, error) {
	if !a.discoverable {
		return nil, nil
	}
	refs := []harness.SessionRef{{RunID: "parent", Files: []string{"parent.json"}}}
	if a.includeChild {
		refs = append(refs, harness.SessionRef{RunID: "child", Files: []string{"child.json"}})
	}
	return refs, nil
}
func (a *nativeWorkIDAdapter) Parse(_ fs.FS, ref harness.SessionRef) (harness.Run, error) {
	return a.runs[ref.RunID], nil
}
func (a *nativeWorkIDAdapter) Mapping() harness.Mapping { return harness.Mapping{} }

func TestNativeWorkIDProcessorToReceiver(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	for _, name := range []string{"parent.json", "child.json"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	registered := registeredWorkIDAdapter(t)
	parent := harness.Run{
		Harness: registered.Name(), SessionID: "parent", Kind: harness.KindAgent, Status: harness.StatusCompleted,
		StartedAt: start, EndedAt: start.Add(time.Minute), WorkstreamID: "file-fallback",
		TurnTimes: []time.Time{start.Add(time.Second)},
		ToolCalls: []harness.ToolCall{{
			ID: "spawn", Name: "Task", StartedAt: start.Add(2 * time.Second), EndedAt: start.Add(3 * time.Second),
			SpawnedSessionIDs: []string{"child"},
		}},
	}
	child := harness.Run{
		Harness: registered.Name(), SessionID: "child", Kind: harness.KindSubagent, Status: harness.StatusCompleted,
		StartedAt: start.Add(4 * time.Second), EndedAt: start.Add(5 * time.Second),
		Parent: &harness.ParentRef{SessionID: "parent", ToolCallID: "spawn"}, TurnTimes: []time.Time{start.Add(4 * time.Second)},
	}
	adapter := &nativeWorkIDAdapter{name: registered.Name(), runs: map[string]harness.Run{"parent": parent, "child": child}}
	config := createDefaultConfig().(*Config)
	config.Home = home
	config.Poll = time.Hour
	core := newCore(config, []harness.Adapter{adapter})
	core.now = func() time.Time { return start.Add(time.Hour) }
	fileTraces := &consumertest.TracesSink{}
	fileMetrics := &consumertest.MetricsSink{}
	core.traces = fileTraces
	core.metrics = fileMetrics

	extensionFactory := registry.NewFactory()
	registryID := component.MustNewID("ember")
	extension, err := extensionFactory.Create(ctx, extensiontest.NewNopSettings(registryID.Type()), extensionFactory.CreateDefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	host := registryHost{extensions: map[component.ID]component.Component{registryID: extension}}
	if err = extension.Start(ctx, host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = extension.Shutdown(context.Background()) })
	sharedRegistry, err := registry.FromHost(host, registryID)
	if err != nil {
		t.Fatal(err)
	}
	core.registry = sharedRegistry
	if err = core.start(home); err != nil {
		t.Fatal(err)
	}
	sharedRegistry.SetSource(core)

	processorFactory := processor.NewFactory()
	processorConfig := processorFactory.CreateDefaultConfig().(*processor.Config)
	processorConfig.Privacy.HashKey = "test-only-key"
	nativeTraces := &consumertest.TracesSink{}
	nativeProcessor, err := processorFactory.CreateTraces(ctx, processortest.NewNopSettings(component.MustNewType("ember")), processorConfig, nativeTraces)
	if err != nil {
		t.Fatal(err)
	}
	if err = nativeProcessor.Start(ctx, host); err != nil {
		t.Fatal(err)
	}
	traces := ptrace.NewTraces()
	resource := traces.ResourceSpans().AppendEmpty()
	mapping := registered.Mapping()
	for key, values := range mapping.Resource {
		resource.Resource().Attributes().PutStr(key, values[0])
	}
	resource.Resource().Attributes().PutStr("ember.work.id", "ISSUE-123")
	span := resource.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetTraceID(pcommon.TraceID{1})
	span.SetSpanID(pcommon.SpanID{1})
	span.SetName("invoke_agent")
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(core.now().Add(-time.Minute)))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(core.now().Add(-time.Minute + time.Second)))
	span.Attributes().PutStr(mapping.SessionKey, "parent")
	if err = nativeProcessor.ConsumeTraces(ctx, traces); err != nil {
		t.Fatal(err)
	}
	if got := nativeSpanWorkIDs(nativeTraces.AllTraces()); len(got) != 1 || got[0] != "" {
		t.Fatalf("unknown native span was enriched before discovery: %v", got)
	}
	if got, ok := sharedRegistry.WorkID(registry.Key{Harness: registered.Name(), RunID: "parent"}); !ok || got != "ISSUE-123" {
		t.Fatalf("pre-discovery native work id = %q, %v", got, ok)
	}

	adapter.discoverable = true
	adapter.includeChild = true
	if err = core.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, got := range nativeSpanWorkIDs(fileTraces.AllTraces()) {
		if got != "ISSUE-123" {
			t.Fatalf("file span work id = %q", got)
		}
	}
	if got := nativeSpanWorkIDs(fileTraces.AllTraces()); len(got) != 3 {
		t.Fatalf("file span count = %d, want parent session/tool and child session", len(got))
	}
	lineage := map[string]string{}
	for _, batch := range fileMetrics.AllMetrics() {
		for _, resource := range batch.ResourceMetrics().All() {
			for _, scope := range resource.ScopeMetrics().All() {
				for _, metric := range scope.Metrics().All() {
					if metric.Name() != "ember.lineage" {
						continue
					}
					for _, point := range metric.Gauge().DataPoints().All() {
						session, _ := point.Attributes().Get("session.id")
						workID, _ := point.Attributes().Get("ember.work.id")
						lineage[session.Str()] = workID.Str()
					}
				}
			}
		}
	}
	if lineage["parent"] != "ISSUE-123" || lineage["child"] != "ISSUE-123" {
		t.Fatalf("lineage work ids = %v", lineage)
	}
	if got := core.state.Runs[stateKey(registered.Name(), "parent")].NativeWorkID; got != "ISSUE-123" {
		t.Fatalf("durable native work id = %q", got)
	}

	restarted := newCore(config, []harness.Adapter{adapter})
	restarted.registry = registry.New()
	restarted.now = core.now
	if err = restarted.start(home); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"parent", "child"} {
		snapshot, ok := restarted.registry.Get(registry.Key{Harness: registered.Name(), RunID: id})
		if !ok || snapshot.WorkID != "ISSUE-123" {
			t.Fatalf("restarted %s work id = %q, %v", id, snapshot.WorkID, ok)
		}
	}
	if _, ok := restarted.registry.WorkID(registry.Key{Harness: registered.Name(), RunID: "child"}); ok {
		t.Fatal("inherited child work id was promoted to native authority")
	}
}

func TestFileWorkIDFallbackIsNotPromoted(t *testing.T) {
	f := newFixture(t, harness.StatusCompleted)
	f.adapter.run.WorkstreamID = "file-fallback"
	f.poll(t)
	key := registry.Key{Harness: "fake", RunID: "run"}
	if _, ok := f.core.registry.WorkID(key); ok {
		t.Fatal("file fallback became native authority")
	}
	if got := f.core.state.Runs[stateKey("fake", "run")].NativeWorkID; got != "" {
		t.Fatalf("durable native work id = %q", got)
	}
	snapshot, ok := f.core.registry.Get(key)
	if !ok || snapshot.WorkID != "file-fallback" {
		t.Fatalf("file fallback snapshot = %+v", snapshot)
	}
}

func registeredWorkIDAdapter(t *testing.T) harness.Adapter {
	t.Helper()
	for _, adapter := range harness.Adapters() {
		mapping := adapter.Mapping()
		if mapping.SessionKey != "" && len(mapping.Resource) != 0 {
			return adapter
		}
	}
	t.Fatal("no registered adapter has native resource and session mappings")
	return nil
}

func nativeSpanWorkIDs(batches []ptrace.Traces) []string {
	var out []string
	for _, batch := range batches {
		for _, resource := range batch.ResourceSpans().All() {
			for _, scope := range resource.ScopeSpans().All() {
				for _, span := range scope.Spans().All() {
					workID, _ := span.Attributes().Get("ember.work.id")
					out = append(out, workID.Str())
				}
			}
		}
	}
	return out
}
