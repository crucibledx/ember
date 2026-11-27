package harness

import "time"

// AgentKind describes a run's position in a multi-agent setup.
type AgentKind string

const (
	KindOrchestrator AgentKind = "orchestrator"
	KindAgent        AgentKind = "agent"
	KindSubagent     AgentKind = "subagent"
	KindSession      AgentKind = "session"
)

// RunStatus is the normalized lifecycle state of a run.
type RunStatus string

const (
	StatusCompleted RunStatus = "completed"
	StatusRunning   RunStatus = "running"
	StatusCancelled RunStatus = "cancelled"
	StatusFailed    RunStatus = "failed"
	StatusUnknown   RunStatus = "unknown"
)

// Usage contains token and credit totals for one model.
type Usage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Credits    float64
}

// ToolCall is the content-free portion of a harness tool invocation.
type ToolCall struct {
	ID        string
	Name      string
	StartedAt time.Time
	// EndedAt is zero while the call is incomplete.
	EndedAt time.Time
	IsError bool
	// SpawnedSessionIDs contains only child ids extracted from spawn results.
	SpawnedSessionIDs []string
}

// ParentRef identifies the parent session and, when known, its spawning tool call.
type ParentRef struct {
	SessionID  string
	ToolCallID string
}

// Run is one normalized harness session. It contains metadata, never prompt or tool content.
type Run struct {
	Harness      string
	SessionID    string
	Kind         AgentKind
	AgentType    string
	WorkstreamID string
	Parent       *ParentRef
	Status       RunStatus
	StartedAt    time.Time
	EndedAt      time.Time
	Model        string
	// Usage is keyed by model. Aggregate-only usage uses the key "unknown".
	Usage     map[string]Usage
	LLMTurns  int
	TurnTimes []time.Time
	ToolCalls []ToolCall

	Cwd    string
	Branch string
	Owner  string
	HostID string
}
