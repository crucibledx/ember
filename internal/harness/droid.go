package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const droidSessionsPath = ".factory/sessions"
const droidTaskInvocationPath = ".factory/state/task-invocations.json"

var (
	droidSessionIDPattern   = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	droidMCPPattern         = regexp.MustCompile(`^(.+?)___(.+)$`)
	droidStatusByInvocation = map[string]RunStatus{
		"completed": StatusCompleted, "running": StatusRunning, "cancelled": StatusCancelled, "failed": StatusFailed, "error": StatusFailed,
	}
)

type droidAdapter struct{}

func init() {
	Register(droidAdapter{})
}

func (droidAdapter) Name() string {
	return "droid"
}

func (droidAdapter) Detect(env Env) Detection {
	onPath := false
	if env.LookPath != nil {
		_, err := env.LookPath("droid")
		onPath = err == nil
	}
	detection := Detection{
		Installed: onPath || (env.FS != nil && droidPathExists(env.FS, ".factory")),
	}
	if !detection.Installed {
		return detection
	}
	detection.ConfigPaths = []string{filepath.Join(env.Home, ".factory", "settings.json")}
	detection.SessionPaths = []string{
		filepath.Join(env.Home, droidSessionsPath),
		filepath.Join(env.Home, droidTaskInvocationPath),
	}
	if onPath && env.Command != nil {
		version, err := env.Command(context.Background(), "droid", "--version")
		if err == nil {
			detection.Version = strings.TrimSpace(version)
		}
	}
	return detection
}

func (droidAdapter) Setup(env Env, opts SetupOptions) []Change {
	changes := droidEnvironmentChanges(EnvShell, "", opts.ListenHTTP, opts.Content)
	if opts.FactoryApp {
		path := filepath.Join(env.Home, ".config", "droid", "daemon.env")
		changes = append(changes, droidEnvironmentChanges(EnvFile, path, opts.ListenHTTP, opts.Content)...)
	}
	return changes
}

func droidEnvironmentChanges(target Target, path, listenHTTP string, content bool) []Change {
	changes := []Change{
		{Target: target, Path: path, Key: "OTEL_TELEMETRY_ENDPOINT", Value: "http://" + listenHTTP, Exclusive: true},
		{Target: target, Path: path, Key: "OTEL_TELEMETRY_FORMAT", Value: "genai"},
	}
	if !content {
		changes = append(changes, Change{Target: target, Path: path, Key: "OTEL_LOG_MESSAGE_CONTENT", Value: "false"})
	}
	return changes
}

func (droidAdapter) Sessions(env Env) ([]SessionRef, error) {
	if env.FS == nil {
		return nil, nil
	}
	projects, err := fs.ReadDir(env.FS, droidSessionsPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read droid sessions directory: %w", err)
	}
	var refs []SessionRef
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		projectPath := path.Join(droidSessionsPath, project.Name())
		files, err := fs.ReadDir(env.FS, projectPath)
		if err != nil {
			return nil, fmt.Errorf("read droid session directory %q: %w", projectPath, err)
		}
		for _, file := range files {
			runID := strings.TrimSuffix(file.Name(), ".jsonl")
			if file.IsDir() || runID == file.Name() || runID == "" {
				continue
			}
			sessionPath := path.Join(projectPath, file.Name())
			settingsPath := strings.TrimSuffix(sessionPath, ".jsonl") + ".settings.json"
			refs = append(refs, SessionRef{
				RunID: runID, Files: []string{sessionPath, settingsPath, droidTaskInvocationPath},
			})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].RunID == refs[j].RunID {
			return refs[i].Files[0] < refs[j].Files[0]
		}
		return refs[i].RunID < refs[j].RunID
	})
	return refs, nil
}

func droidPathExists(fsys fs.FS, name string) bool {
	_, err := fs.Stat(fsys, name)
	return err == nil
}

type droidSettings struct {
	Model      string `json:"model"`
	TokenUsage *struct {
		InputTokens         int64   `json:"inputTokens"`
		OutputTokens        int64   `json:"outputTokens"`
		CacheReadTokens     int64   `json:"cacheReadTokens"`
		CacheCreationTokens int64   `json:"cacheCreationTokens"`
		FactoryCredits      float64 `json:"factoryCredits"`
	} `json:"tokenUsage"`
	Tags []struct {
		Name     string `json:"name"`
		Metadata struct {
			Kind      string `json:"kind"`
			MissionID string `json:"missionId"`
		} `json:"metadata"`
	} `json:"tags"`
}

type droidInvocation struct {
	ChildSessionID  string `json:"childSessionId"`
	ParentSessionID string `json:"parentSessionId"`
	ParentToolUseID string `json:"parentToolUseId"`
	SubagentType    string `json:"subagentType"`
	Status          string `json:"status"`
}

type droidSessionStart struct {
	CallingSessionID   string
	Cwd, Owner, HostID string
}

func (droidAdapter) Parse(fsys fs.FS, ref SessionRef) (Run, error) {
	sessionFile := droidRefFile(ref, ".jsonl")
	if sessionFile == "" {
		return Run{}, fmt.Errorf("parse droid session %q: no session file", ref.RunID)
	}
	data, err := fs.ReadFile(fsys, sessionFile)
	if err != nil {
		return Run{}, fmt.Errorf("read droid session %q: %w", ref.RunID, err)
	}

	settings, err := droidReadSettings(fsys, ref)
	if err != nil {
		return Run{}, fmt.Errorf("parse droid session %q: %w", ref.RunID, err)
	}
	invocations, err := droidReadInvocations(fsys, ref)
	if err != nil {
		return Run{}, fmt.Errorf("parse droid session %q: %w", ref.RunID, err)
	}
	invocation, hasInvocation := invocations[ref.RunID]
	var start droidSessionStart
	var startedAt, endedAt time.Time
	var model string
	var turnTimes []time.Time
	calls := map[string]ToolCall{}

	droidScanRecords(data, &start, &startedAt, &endedAt, &model, &turnTimes, calls)
	if startedAt.IsZero() {
		return Run{}, fmt.Errorf("parse droid session %q: no timestamped records", ref.RunID)
	}

	kind, workstreamID := droidKindAndWorkstream(settings, ref.RunID, hasInvocation)
	parentSessionID := start.CallingSessionID
	parentToolCallID := ""
	if hasInvocation {
		parentSessionID = invocation.ParentSessionID
		parentToolCallID = invocation.ParentToolUseID
	}
	run := Run{
		Harness:      "droid",
		SessionID:    ref.RunID,
		Kind:         kind,
		WorkstreamID: workstreamID,
		Status:       droidInvocationStatus(invocation.Status, hasInvocation),
		StartedAt:    startedAt,
		EndedAt:      endedAt,
		Model:        model,
		LLMTurns:     len(turnTimes),
		TurnTimes:    turnTimes,
		ToolCalls:    droidSortedToolCalls(calls),
		Cwd:          start.Cwd,
		Owner:        start.Owner,
		HostID:       start.HostID,
	}
	if hasInvocation {
		run.AgentType = invocation.SubagentType
	}
	if parentSessionID != "" {
		run.Parent = &ParentRef{SessionID: parentSessionID, ToolCallID: parentToolCallID}
	}
	if run.Model == "" {
		run.Model = settings.Model
	}
	if settings.TokenUsage != nil {
		usage := settings.TokenUsage
		run.Usage = map[string]Usage{"unknown": {
			Input:      usage.InputTokens,
			Output:     usage.OutputTokens,
			CacheRead:  usage.CacheReadTokens,
			CacheWrite: usage.CacheCreationTokens,
			Credits:    usage.FactoryCredits,
		}}
	}
	return run, nil
}

func droidScanRecords(data []byte, start *droidSessionStart, startedAt, endedAt *time.Time, model *string, turnTimes *[]time.Time, calls map[string]ToolCall) {
	for _, line := range strings.Split(string(data), "\n") {
		var envelope struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil || envelope.Type == "" {
			continue
		}
		if envelope.Type == "session_start" {
			if parsed, ok := droidParseSessionStart([]byte(line)); ok {
				*start = parsed
			}
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err != nil {
			continue
		}
		if startedAt.IsZero() || at.Before(*startedAt) {
			*startedAt = at
		}
		if endedAt.IsZero() || at.After(*endedAt) {
			*endedAt = at
		}
		if envelope.Type == "message" {
			droidApplyMessage([]byte(line), at, model, turnTimes, calls)
		}
	}
}

func droidParseSessionStart(data []byte) (droidSessionStart, bool) {
	var record struct {
		ID               string  `json:"id"`
		CallingSessionID *string `json:"callingSessionId"`
		Cwd              string  `json:"cwd"`
		Owner            string  `json:"owner"`
		HostID           string  `json:"hostId"`
	}
	if json.Unmarshal(data, &record) != nil || record.ID == "" {
		return droidSessionStart{}, false
	}
	start := droidSessionStart{Cwd: record.Cwd, Owner: record.Owner, HostID: record.HostID}
	if record.CallingSessionID != nil {
		start.CallingSessionID = *record.CallingSessionID
	}
	return start, true
}

func droidApplyMessage(data []byte, at time.Time, model *string, turnTimes *[]time.Time, calls map[string]ToolCall) {
	var record struct {
		Message struct {
			Role    string          `json:"role"`
			ModelID string          `json:"modelId"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &record) != nil {
		return
	}
	var content *[]json.RawMessage
	if json.Unmarshal(record.Message.Content, &content) != nil || content == nil {
		return
	}
	role := record.Message.Role
	if role == "assistant" {
		*turnTimes = append(*turnTimes, at)
		if record.Message.ModelID != "" {
			*model = record.Message.ModelID
		}
	}
	for _, raw := range *content {
		var block struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &block) != nil {
			continue
		}
		switch block.Type {
		case "tool_use":
			droidApplyToolUse(raw, role, at, calls)
		case "tool_result":
			droidApplyToolResult(raw, role, at, calls)
		}
	}
}

func droidApplyToolUse(raw json.RawMessage, role string, at time.Time, calls map[string]ToolCall) {
	if role != "assistant" {
		return
	}
	var block struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &block) != nil || block.ID == "" || block.Name == "" {
		return
	}
	calls[block.ID] = ToolCall{ID: block.ID, Name: block.Name, StartedAt: at}
}

func droidApplyToolResult(raw json.RawMessage, role string, at time.Time, calls map[string]ToolCall) {
	if role != "user" {
		return
	}
	var block struct {
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &block) != nil {
		return
	}
	call, ok := calls[block.ToolUseID]
	if !ok {
		return
	}
	call.EndedAt = at
	call.IsError = block.IsError
	if call.Name == "SpawnAgent" {
		call.SpawnedSessionIDs = droidSessionIDs(block.Content)
	}
	calls[block.ToolUseID] = call
}

func droidSessionIDs(content json.RawMessage) []string {
	matches := droidSessionIDPattern.FindAllString(string(content), -1)
	ids := make([]string, 0, len(matches))
	for _, id := range matches {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func droidRefFile(ref SessionRef, suffix string) string {
	for _, name := range ref.Files {
		if strings.HasSuffix(name, suffix) {
			return name
		}
	}
	return ""
}

func droidReadSettings(fsys fs.FS, ref SessionRef) (droidSettings, error) {
	var settings droidSettings
	name := droidRefFile(ref, ".settings.json")
	if name == "" {
		return settings, nil
	}
	data, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read droid settings: %w", err)
	}
	if json.Unmarshal(data, &settings) != nil {
		return droidSettings{}, nil
	}
	return settings, nil
}

func droidReadInvocations(fsys fs.FS, ref SessionRef) (map[string]droidInvocation, error) {
	byChild := map[string]droidInvocation{}
	if !slices.Contains(ref.Files, droidTaskInvocationPath) {
		return byChild, nil
	}
	data, err := fs.ReadFile(fsys, droidTaskInvocationPath)
	if errors.Is(err, fs.ErrNotExist) {
		return byChild, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read droid task invocations: %w", err)
	}
	var file struct {
		Invocations []json.RawMessage `json:"invocations"`
	}
	if json.Unmarshal(data, &file) != nil {
		return byChild, nil
	}
	for _, raw := range file.Invocations {
		var invocation droidInvocation
		if json.Unmarshal(raw, &invocation) != nil || invocation.ChildSessionID == "" ||
			invocation.ParentSessionID == "" || invocation.ParentToolUseID == "" {
			continue
		}
		if _, exists := byChild[invocation.ChildSessionID]; !exists {
			byChild[invocation.ChildSessionID] = invocation
		}
	}
	return byChild, nil
}

func droidKindAndWorkstream(settings droidSettings, sessionID string, hasInvocation bool) (AgentKind, string) {
	missionID := ""
	missionKind := ""
	missionSeen := false
	subagent := hasInvocation
	for _, tag := range settings.Tags {
		switch tag.Name {
		case "mission-v2":
			if !missionSeen {
				missionID = tag.Metadata.MissionID
				missionKind = tag.Metadata.Kind
				missionSeen = true
			}
		case "subagent":
			subagent = true
		}
	}
	switch {
	case missionKind == string(KindOrchestrator):
		if missionID == "" {
			missionID = sessionID
		}
		return KindOrchestrator, missionID
	case missionKind == string(KindAgent):
		return KindAgent, missionID
	case subagent:
		return KindSubagent, missionID
	default:
		return KindSession, missionID
	}
}

func droidInvocationStatus(status string, present bool) RunStatus {
	if present {
		if normalized, ok := droidStatusByInvocation[status]; ok {
			return normalized
		}
	}
	return StatusUnknown
}

func droidSortedToolCalls(calls map[string]ToolCall) []ToolCall {
	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, call)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

func (droidAdapter) Mapping() Mapping {
	return Mapping{
		Resource:     map[string][]string{"service.name": {"cli"}},
		SessionKey:   "session.id",
		ToolCallKeys: []string{"gen_ai.tool.call.id"},
		ToolNameKeys: []string{"gen_ai.tool.name"},
		ToolSpanName: "execute_tool ",
		MCP:          droidMCPPattern,
		NativeAllow:  strings.Fields("user.id organization.id model.id parent_session.id gen_ai.operation.name gen_ai.request.model gen_ai.response.model gen_ai.provider.name gen_ai.tool.type gen_ai.agent.name tool.category tool.runtime tool.succeeded session.autonomy_mode environment.type skill.location hook.event_name mcp.server terminal.type"),
		OwnerKeys:    []string{"user.id"},
	}
}
