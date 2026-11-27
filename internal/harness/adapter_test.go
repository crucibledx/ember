package harness_test

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/harness/harnesstest"
)

func TestFakeAdapterConformance(t *testing.T) {
	harnesstest.Run(t, func() harness.Adapter {
		return harnesstest.NewFake()
	})
}

func TestRegisteredAdaptersConformance(t *testing.T) {
	for _, registered := range harness.Adapters() {
		t.Run(registered.Name(), func(t *testing.T) {
			harnesstest.Run(t, func() harness.Adapter {
				return registered
			})
		})
	}
}

func TestFakeAdapterFixture(t *testing.T) {
	adapter := harnesstest.NewFake()
	refs, err := adapter.Sessions(harness.Env{})
	if err != nil {
		t.Fatalf("Sessions(): %v", err)
	}
	run, err := adapter.Parse(os.DirFS("harnesstest/testdata"), refs[0])
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}

	if run.Harness != adapter.Name() || run.SessionID != "session-1" || run.Kind != harness.KindSubagent {
		t.Errorf("unexpected identity: %+v", run)
	}
	if run.Parent == nil || run.Parent.SessionID != "parent-1" || run.Parent.ToolCallID != "spawn-1" {
		t.Errorf("unexpected parent: %+v", run.Parent)
	}
	if run.Status != harness.StatusCompleted || run.StartedAt.IsZero() || !run.EndedAt.After(run.StartedAt) {
		t.Errorf("unexpected lifecycle: %+v", run)
	}
	wantUsage := harness.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Credits: 1.5}
	if !reflect.DeepEqual(run.Usage["model-v1"], wantUsage) {
		t.Errorf("usage = %+v, want %+v", run.Usage["model-v1"], wantUsage)
	}
	if run.LLMTurns != 1 || len(run.TurnTimes) != 1 {
		t.Errorf("unexpected turns: %+v", run)
	}
	if len(run.ToolCalls) != 1 || run.ToolCalls[0].EndedAt.IsZero() {
		t.Fatalf("unexpected tool calls: %+v", run.ToolCalls)
	}
	if !reflect.DeepEqual(run.ToolCalls[0].SpawnedSessionIDs, []string{"child-1"}) {
		t.Errorf("spawned ids = %v", run.ToolCalls[0].SpawnedSessionIDs)
	}
	if run.Cwd != "/workspace/project" || run.Branch != "feature/test" || run.Owner != "owner-1" || run.HostID != "host-1" {
		t.Errorf("unexpected metadata: %+v", run)
	}
}

func TestFakeAdapterEnvironmentAndSetup(t *testing.T) {
	adapter := harnesstest.NewFake()
	env := harness.Env{
		Home: t.TempDir(),
		Getenv: func(string) string {
			return "1"
		},
	}
	detection := adapter.Detect(env)
	if !detection.Installed || detection.Version == "" || len(detection.ConfigPaths) != 1 || len(detection.SessionPaths) != 1 {
		t.Errorf("Detect() = %+v", detection)
	}

	changes := adapter.Setup(env, harness.SetupOptions{ListenHTTP: "127.0.0.1:4318", FactoryApp: true})
	if len(changes) != 3 {
		t.Fatalf("Setup() returned %d changes, want 3: %+v", len(changes), changes)
	}
	if changes[0].Target != harness.EnvShell || !changes[0].Exclusive || changes[0].HeadersKey == "" || changes[0].ProtocolKey == "" {
		t.Errorf("unexpected shell change: %+v", changes[0])
	}
	if changes[1].Target != harness.EnvFile || changes[1].Path == "" {
		t.Errorf("unexpected env file change: %+v", changes[1])
	}
	if changes[2].Target != harness.JSONKey || changes[2].Key != "telemetry.content" || changes[2].Value != "false" {
		t.Errorf("unexpected content change: %+v", changes[2])
	}

	contentChanges := adapter.Setup(env, harness.SetupOptions{Content: true})
	for _, change := range contentChanges {
		if change.Key == "telemetry.content" {
			t.Errorf("Setup() disabled content with Content=true: %+v", change)
		}
	}
}

func TestFakeAdapterMapping(t *testing.T) {
	mapping := harnesstest.NewFake().Mapping()
	if got := mapping.Resource["service.name"]; !reflect.DeepEqual(got, []string{"fake-service"}) {
		t.Errorf("resource values = %v", got)
	}
	if mapping.Metrics.Cost != "fake.cost.usage" || mapping.Metrics.CostUnit != "USD" {
		t.Errorf("metrics = %+v", mapping.Metrics)
	}
	match := mapping.MCP.FindStringSubmatch("mcp__server__tool")
	if !reflect.DeepEqual(match, []string{"mcp__server__tool", "server", "tool"}) {
		t.Errorf("MCP match = %v", match)
	}
}

func TestRunTimestampsRoundTripThroughFixture(t *testing.T) {
	adapter := harnesstest.NewFake()
	run, err := adapter.Parse(os.DirFS("harnesstest/testdata"), harness.SessionRef{
		RunID: "session-1",
		Files: []string{"session.json"},
	})
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	want := time.Date(2026, time.November, 1, 10, 0, 0, 0, time.UTC)
	if !run.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %s, want %s", run.StartedAt, want)
	}
}
