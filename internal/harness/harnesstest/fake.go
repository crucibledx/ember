package harnesstest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"

	"github.com/crucibledx/ember/internal/harness"
)

var fakeMCP = regexp.MustCompile(`^mcp__(.+?)__(.+)$`)

// Fake is an Adapter test double whose Parse method reads testdata/session.json.
type Fake struct{}

// NewFake returns a ready fake adapter.
func NewFake() Fake {
	return Fake{}
}

func (Fake) Name() string {
	return "fake"
}

func (Fake) Detect(env harness.Env) harness.Detection {
	installed := env.Getenv("FAKE_INSTALLED") == "1"
	detection := harness.Detection{Installed: installed}
	if installed {
		detection.Version = "1.0.0"
		detection.ConfigPaths = []string{filepath.Join(env.Home, ".fake", "settings.json")}
		detection.SessionPaths = []string{filepath.Join(env.Home, ".fake", "sessions")}
	}
	return detection
}

func (Fake) Setup(env harness.Env, opts harness.SetupOptions) []harness.Change {
	changes := []harness.Change{{
		Target:      harness.EnvShell,
		Key:         "FAKE_OTEL_ENDPOINT",
		Value:       "http://" + opts.ListenHTTP,
		Exclusive:   true,
		HeadersKey:  "FAKE_OTEL_HEADERS",
		ProtocolKey: "FAKE_OTEL_PROTOCOL",
	}}
	if opts.FactoryApp {
		changes = append(changes, harness.Change{
			Target: harness.EnvFile,
			Path:   filepath.Join(env.Home, ".fake", "daemon.env"),
			Key:    "FAKE_OTEL_ENDPOINT",
			Value:  "http://" + opts.ListenHTTP,
		})
	}
	if !opts.Content {
		changes = append(changes, harness.Change{
			Target: harness.JSONKey,
			Path:   filepath.Join(env.Home, ".fake", "settings.json"),
			Key:    "telemetry.content",
			Value:  "false",
		})
	}
	return changes
}

func (Fake) Sessions(harness.Env) ([]harness.SessionRef, error) {
	return []harness.SessionRef{{RunID: "session-1", Files: []string{"session.json"}}}, nil
}

func (Fake) Parse(fsys fs.FS, ref harness.SessionRef) (harness.Run, error) {
	if len(ref.Files) == 0 {
		return harness.Run{}, fmt.Errorf("parse fake session %q: no files", ref.RunID)
	}
	data, err := fs.ReadFile(fsys, ref.Files[0])
	if err != nil {
		return harness.Run{}, fmt.Errorf("read fake session %q: %w", ref.RunID, err)
	}
	var run harness.Run
	if err := json.Unmarshal(data, &run); err != nil {
		return harness.Run{}, fmt.Errorf("decode fake session %q: %w", ref.RunID, err)
	}
	if run.SessionID != ref.RunID {
		return harness.Run{}, fmt.Errorf("decode fake session %q: session id %q does not match", ref.RunID, run.SessionID)
	}
	return run, nil
}

func (Fake) Mapping() harness.Mapping {
	return harness.Mapping{
		Resource:      map[string][]string{"service.name": {"fake-service"}},
		SessionKey:    "session.id",
		SubagentKey:   "agent_id",
		ToolCallKeys:  []string{"tool_use_id"},
		ToolNameKeys:  []string{"tool_name"},
		ToolSpanName:  "fake.tool",
		RedactedTools: []string{"mcp_tool"},
		MCP:           fakeMCP,
		NativeAllow:   []string{"tool_name", "success"},
		ContentKeys:   []string{"prompt", "tool_input"},
		OwnerKeys:     []string{"user.id"},
		Metrics:       harness.NativeMetrics{Cost: "fake.cost.usage", CostUnit: "USD"},
	}
}
