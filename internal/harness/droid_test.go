package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/harness/harnesstest"
)

const (
	droidOrchestratorID = "00000000-0000-4000-8000-000000000001"
	droidAgentID        = "00000000-0000-4000-8000-000000000002"
	droidSubagentID     = "00000000-0000-4000-8000-000000000003"
	droidFixtureSecret  = "TOP-SECRET-PROMPT-TEXT"
)

func TestDroidAdapterConformance(t *testing.T) {
	adapter := droidAdapterUnderTest(t)
	harnesstest.Run(t, func() harness.Adapter { return adapter })
}

func TestDroidSessions(t *testing.T) {
	adapter := droidAdapterUnderTest(t)
	refs, err := adapter.Sessions(droidFixtureEnv())
	if err != nil {
		t.Fatalf("Sessions(): %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("Sessions() returned %d refs, want 3: %+v", len(refs), refs)
	}
	wantIDs := []string{droidOrchestratorID, droidAgentID, droidSubagentID}
	for i, ref := range refs {
		if ref.RunID != wantIDs[i] {
			t.Errorf("ref %d RunID = %q, want %q", i, ref.RunID, wantIDs[i])
		}
		if len(ref.Files) != 3 || !strings.HasSuffix(ref.Files[0], ".jsonl") ||
			!strings.HasSuffix(ref.Files[1], ".settings.json") || ref.Files[2] != ".factory/state/task-invocations.json" {
			t.Errorf("ref %d files = %v, want session, settings and invocations", i, ref.Files)
		}
		for _, name := range ref.Files {
			if !fs.ValidPath(name) {
				t.Errorf("invalid slash path %q", name)
			}
		}
	}
}

func TestDroidParse(t *testing.T) {
	runs := droidFixtureRuns(t)

	t.Run("orchestrator and spawn link", func(t *testing.T) {
		run := runs[droidOrchestratorID]
		if run.Harness != "droid" || run.Kind != harness.KindOrchestrator || run.WorkstreamID != "w-1" {
			t.Errorf("unexpected identity: %+v", run)
		}
		if run.Parent != nil || run.Status != harness.StatusUnknown {
			t.Errorf("unexpected parent or status: %+v, %s", run.Parent, run.Status)
		}
		if run.Cwd != "/workspace/orchestrator" || run.Owner != "owner-1" || run.HostID != "host-1" {
			t.Errorf("unexpected session metadata: %+v", run)
		}
		if len(run.ToolCalls) != 1 || run.ToolCalls[0].Name != "SpawnAgent" {
			t.Fatalf("unexpected tool calls: %+v", run.ToolCalls)
		}
		if !reflect.DeepEqual(run.ToolCalls[0].SpawnedSessionIDs, []string{droidAgentID}) {
			t.Errorf("spawned ids = %v, want %s", run.ToolCalls[0].SpawnedSessionIDs, droidAgentID)
		}
		wantStart := time.Date(2026, time.November, 11, 10, 0, 0, 0, time.UTC)
		wantEnd := wantStart.Add(2 * time.Minute)
		if !run.StartedAt.Equal(wantStart) || !run.EndedAt.Equal(wantEnd) {
			t.Errorf("lifecycle = %s to %s, want %s to %s", run.StartedAt, run.EndedAt, wantStart, wantEnd)
		}
	})

	t.Run("agent usage tools and caller", func(t *testing.T) {
		run := runs[droidAgentID]
		if run.Kind != harness.KindAgent || run.WorkstreamID != "w-1" || run.Model != "model-x" {
			t.Errorf("unexpected identity: %+v", run)
		}
		if run.Parent == nil || run.Parent.SessionID != droidOrchestratorID || run.Parent.ToolCallID != "" {
			t.Errorf("unexpected parent: %+v", run.Parent)
		}
		wantUsage := map[string]harness.Usage{"unknown": {
			Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Credits: 1.25,
		}}
		if !reflect.DeepEqual(run.Usage, wantUsage) {
			t.Errorf("usage = %+v, want %+v", run.Usage, wantUsage)
		}
		if run.LLMTurns != 1 || len(run.TurnTimes) != 1 {
			t.Errorf("unexpected turns: %d, %v", run.LLMTurns, run.TurnTimes)
		}
		if len(run.ToolCalls) != 2 {
			t.Fatalf("tool calls = %+v, want Read and Task", run.ToolCalls)
		}
		read, task := run.ToolCalls[0], run.ToolCalls[1]
		if read.Name != "Read" || !read.IsError || read.EndedAt.Sub(read.StartedAt) != time.Minute {
			t.Errorf("Read call = %+v", read)
		}
		if task.Name != "Task" || task.IsError || task.EndedAt.Sub(task.StartedAt) != 6*time.Minute {
			t.Errorf("Task call = %+v", task)
		}
		if run.Cwd != "/workspace/agent" || run.Owner != "owner-1" || run.HostID != "host-1" {
			t.Errorf("unexpected session metadata: %+v", run)
		}
	})

	t.Run("Task subagent uses first invocation", func(t *testing.T) {
		run := runs[droidSubagentID]
		if run.Kind != harness.KindSubagent || run.AgentType != "worker" || run.Status != harness.StatusCompleted {
			t.Errorf("unexpected identity: %+v", run)
		}
		if run.Parent == nil || run.Parent.SessionID != droidAgentID || run.Parent.ToolCallID != "toolu_task" {
			t.Errorf("parent = %+v, want first Task invocation", run.Parent)
		}
		if run.Model != "model-y" || run.LLMTurns != 1 || run.Cwd != "/workspace/subagent" || run.HostID != "host-2" {
			t.Errorf("unexpected model, turns or metadata: %+v", run)
		}
	})
	t.Run("null assistant content is skipped", func(t *testing.T) {
		run := droidParseSynthetic(t, `{"type":"message","timestamp":"2026-11-11T10:00:00Z","message":{"role":"assistant","modelId":"bad-model","content":null}}
{"type":"message","timestamp":"2026-11-11T10:01:00Z","message":{"role":"user","content":[]}}
`, `{}`)
		if run.LLMTurns != 0 || len(run.TurnTimes) != 0 || run.Model != "" {
			t.Errorf("null-content assistant changed turns or model: %+v", run)
		}
	})

	t.Run("first mission tag wins", func(t *testing.T) {
		settings := `{"tags":[{"name":"mission-v2","metadata":{"kind":"agent","missionId":"w-first"}},{"name":"mission-v2","metadata":{"kind":"orchestrator","missionId":"w-second"}}]}`
		run := droidParseSynthetic(t, `{"type":"message","timestamp":"2026-11-11T10:00:00Z","message":{"role":"user","content":[]}}
`, settings)
		if run.Kind != harness.KindAgent || run.WorkstreamID != "w-first" {
			t.Errorf("duplicate mission tags produced kind %s, workstream %s", run.Kind, run.WorkstreamID)
		}
	})
}

func TestDroidParseNoContent(t *testing.T) {
	for id, run := range droidFixtureRuns(t) {
		serialized, err := json.Marshal(run)
		if err != nil {
			t.Fatalf("marshal %s: %v", id, err)
		}
		for _, forbidden := range []string{droidFixtureSecret, "/secret/path"} {
			if strings.Contains(string(serialized), forbidden) {
				t.Errorf("run %s contains forbidden content %q: %s", id, forbidden, serialized)
			}
		}
	}
}

func TestDroidParseWithoutUsableRecords(t *testing.T) {
	adapter := droidAdapterUnderTest(t)
	fsys := fstest.MapFS{
		".factory/sessions/project/empty.jsonl": {Data: []byte("not json\n{\"type\":\"session_start\",\"id\":\"empty\"}\n")},
	}
	_, err := adapter.Parse(fsys, harness.SessionRef{
		RunID: "empty",
		Files: []string{".factory/sessions/project/empty.jsonl"},
	})
	if err == nil || !strings.Contains(err.Error(), "no timestamped records") {
		t.Fatalf("Parse() error = %v, want no timestamped records", err)
	}
}

func TestDroidSetup(t *testing.T) {
	adapter := droidAdapterUnderTest(t)

	t.Run("Detect on PATH", func(t *testing.T) {
		home := t.TempDir()
		var commandName string
		var commandArgs []string
		detection := adapter.Detect(harness.Env{
			Home: home,
			LookPath: func(name string) (string, error) {
				if name != "droid" {
					t.Errorf("LookPath name = %q", name)
				}
				return "/bin/droid", nil
			},
			Command: func(_ context.Context, name string, args ...string) (string, error) {
				commandName = name
				commandArgs = args
				return "1.8.0\n", nil
			},
		})
		if !detection.Installed || detection.Version != "1.8.0" {
			t.Errorf("Detect() = %+v", detection)
		}
		if commandName != "droid" || !reflect.DeepEqual(commandArgs, []string{"--version"}) {
			t.Errorf("Command = %s %v", commandName, commandArgs)
		}
		wantConfig := []string{filepath.Join(home, ".factory", "settings.json")}
		wantSessions := []string{
			filepath.Join(home, ".factory", "sessions"),
			filepath.Join(home, ".factory", "state", "task-invocations.json"),
		}
		if !reflect.DeepEqual(detection.ConfigPaths, wantConfig) || !reflect.DeepEqual(detection.SessionPaths, wantSessions) {
			t.Errorf("Detect() paths = %v, %v", detection.ConfigPaths, detection.SessionPaths)
		}
	})

	t.Run("Detect from factory directory", func(t *testing.T) {
		commandCalled := false
		detection := adapter.Detect(harness.Env{
			Home:     "testdata/droid",
			FS:       os.DirFS("testdata/droid"),
			LookPath: func(string) (string, error) { return "", errors.New("not installed") },
			Command: func(context.Context, string, ...string) (string, error) {
				commandCalled = true
				return "", nil
			},
		})
		if !detection.Installed || commandCalled {
			t.Errorf("Detect() = %+v, command called = %t", detection, commandCalled)
		}
	})

	t.Run("Setup shell and Factory App", func(t *testing.T) {
		home := t.TempDir()
		changes := adapter.Setup(harness.Env{Home: home}, harness.SetupOptions{ListenHTTP: "127.0.0.1:4318"})
		want := []harness.Change{
			{Target: harness.EnvShell, Key: "OTEL_TELEMETRY_ENDPOINT", Value: "http://127.0.0.1:4318", Exclusive: true},
			{Target: harness.EnvShell, Key: "OTEL_TELEMETRY_FORMAT", Value: "genai"},
			{Target: harness.EnvShell, Key: "OTEL_LOG_MESSAGE_CONTENT", Value: "false"},
		}
		if !reflect.DeepEqual(changes, want) {
			t.Errorf("Setup() = %+v, want %+v", changes, want)
		}
		changes = adapter.Setup(harness.Env{Home: home}, harness.SetupOptions{
			ListenHTTP: "127.0.0.1:4318", FactoryApp: true,
		})
		if len(changes) != 6 {
			t.Fatalf("Setup(FactoryApp) returned %d changes: %+v", len(changes), changes)
		}
		daemonPath := filepath.Join(home, ".config", "droid", "daemon.env")
		for i, change := range changes[3:] {
			wantChange := want[i]
			wantChange.Target = harness.EnvFile
			wantChange.Path = daemonPath
			if !reflect.DeepEqual(change, wantChange) {
				t.Errorf("daemon change %d = %+v, want %+v", i, change, wantChange)
			}
		}
	})
	t.Run("Setup leaves content enabled", func(t *testing.T) {
		for _, factoryApp := range []bool{false, true} {
			changes := adapter.Setup(harness.Env{Home: t.TempDir()}, harness.SetupOptions{
				ListenHTTP: "127.0.0.1:4318", FactoryApp: factoryApp, Content: true,
			})
			wantCount := 2
			if factoryApp {
				wantCount = 4
			}
			if len(changes) != wantCount {
				t.Errorf("Setup(Content=true, FactoryApp=%t) returned %d changes, want %d", factoryApp, len(changes), wantCount)
			}
			for _, change := range changes {
				if change.Key == "OTEL_LOG_MESSAGE_CONTENT" {
					t.Errorf("Setup(Content=true, FactoryApp=%t) changed content: %+v", factoryApp, change)
				}
			}
		}
	})

	t.Run("Mapping", func(t *testing.T) {
		mapping := adapter.Mapping()
		want := harness.Mapping{
			Resource:     map[string][]string{"service.name": {"cli"}},
			SessionKey:   "session.id",
			ToolCallKeys: []string{"gen_ai.tool.call.id"},
			ToolNameKeys: []string{"gen_ai.tool.name"},
			ToolSpanName: "execute_tool ",
			MCP:          mapping.MCP,
			OwnerKeys:    []string{"user.id"},
			NativeAllow: []string{
				"user.id", "organization.id", "model.id", "parent_session.id",
				"gen_ai.operation.name", "gen_ai.request.model", "gen_ai.response.model", "gen_ai.provider.name",
				"gen_ai.tool.type", "gen_ai.agent.name", "tool.category", "tool.runtime", "tool.succeeded",
				"session.autonomy_mode", "environment.type", "skill.location", "hook.event_name", "mcp.server", "terminal.type",
			},
		}
		if !reflect.DeepEqual(mapping, want) {
			t.Errorf("Mapping() = %+v, want %+v", mapping, want)
		}
		if mapping.MCP == nil {
			t.Fatal("Mapping().MCP is nil")
		}
		if mapping.MCP.String() != "^(.+?)___(.+)$" {
			t.Errorf("MCP regex = %q", mapping.MCP.String())
		}
		match := mapping.MCP.FindStringSubmatch("server___tool___extra")
		if !reflect.DeepEqual(match, []string{"server___tool___extra", "server", "tool___extra"}) {
			t.Errorf("MCP match = %v", match)
		}
		if mapping.MCP.MatchString("server_tool") {
			t.Error("MCP regex matched a name without a separator")
		}
	})
}

func droidAdapterUnderTest(t *testing.T) harness.Adapter {
	t.Helper()
	adapter, ok := harness.Lookup("droid")
	if !ok {
		t.Fatal("droid adapter is not registered")
	}
	return adapter
}

func droidFixtureRuns(t *testing.T) map[string]harness.Run {
	t.Helper()
	adapter := droidAdapterUnderTest(t)
	env := droidFixtureEnv()
	refs, err := adapter.Sessions(env)
	if err != nil {
		t.Fatalf("Sessions(): %v", err)
	}
	runs := map[string]harness.Run{}
	for _, ref := range refs {
		run, err := adapter.Parse(env.FS, ref)
		if err != nil {
			t.Fatalf("Parse(%s): %v", ref.RunID, err)
		}
		runs[ref.RunID] = run
	}
	return runs
}

func droidFixtureEnv() harness.Env {
	return harness.Env{Home: "testdata/droid", FS: os.DirFS("testdata/droid")}
}

func droidParseSynthetic(t *testing.T, session, settings string) harness.Run {
	t.Helper()
	sessionPath := ".factory/sessions/project/s.jsonl"
	settingsPath := ".factory/sessions/project/s.settings.json"
	fsys := fstest.MapFS{
		sessionPath:  {Data: []byte(session)},
		settingsPath: {Data: []byte(settings)},
	}
	run, err := droidAdapterUnderTest(t).Parse(fsys, harness.SessionRef{
		RunID: "s", Files: []string{sessionPath, settingsPath},
	})
	if err != nil {
		t.Fatalf("Parse(synthetic): %v", err)
	}
	return run
}
