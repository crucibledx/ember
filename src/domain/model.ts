export const HARNESSES = {DROID: 'droid', CLAUDE_CODE: 'claude-code'} as const
export type Harness = (typeof HARNESSES)[keyof typeof HARNESSES]

/** Where a run sits in a multi-agent setup. `session` is a plain run with no orchestration around it. */
export const AGENT_KINDS = {
  ORCHESTRATOR: 'orchestrator',
  AGENT: 'agent',
  SUBAGENT: 'subagent',
  SESSION: 'session',
} as const
export type AgentKind = (typeof AGENT_KINDS)[keyof typeof AGENT_KINDS]

export const RUN_STATUSES = {
  COMPLETED: 'completed',
  RUNNING: 'running',
  CANCELLED: 'cancelled',
  FAILED: 'failed',
  UNKNOWN: 'unknown',
} as const
export type RunStatus = (typeof RUN_STATUSES)[keyof typeof RUN_STATUSES]

export interface TokenUsage {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
}

export interface ToolCall {
  /** The harness's own tool call id (Anthropic `tool_use.id`); unique within a harness. */
  id: string
  name: string
  startedAt: number
  endedAt?: number
  isError: boolean
  /**
   * Session ids this call started, when the harness records them in the tool result rather than in a
   * side table (Droid `SpawnAgent`). Only ids are kept; the result text is discarded.
   */
  spawnedSessionIds: string[]
}

export interface ParentRef {
  sessionId: string
  /** The exact tool call in the parent that started this run, when the harness records it. */
  toolCallId?: string
}

/** One agent run (a harness session), normalised across harnesses. Content-free by construction. */
export interface AgentRun {
  harness: Harness
  sessionId: string
  kind: AgentKind
  /** Subagent or custom agent type (`worker`, `code-reviewer`), when known. */
  agentType?: string
  workstreamId?: string
  parent?: ParentRef
  status: RunStatus
  startedAt: number
  endedAt: number
  model?: string
  tokens?: TokenUsage
  llmTurns: number
  toolCalls: ToolCall[]
}
