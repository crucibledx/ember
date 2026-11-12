import {
  AGENT_KINDS,
  type AgentKind,
  type AgentRun,
  HARNESSES,
  type RunStatus,
  RUN_STATUSES,
  type ToolCall,
} from '../../model'
import {
  anyRecordSchema,
  contentBlockSchema,
  type DroidSettings,
  messageRecordSchema,
  sessionStartSchema,
  settingsSchema,
  type TaskInvocation,
  taskInvocationSchema,
  taskInvocationsFileSchema,
} from './schema'

/** Tools whose result names the session they started (the orchestrator's agents). */
const SPAWN_TOOLS = new Set(['SpawnAgent'])
const SESSION_ID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g
const MISSION_TAG = 'mission-v2'
const SUBAGENT_TAG = 'subagent'

export interface DroidSessionInput {
  sessionId: string
  /** Raw JSONL lines of `<id>.jsonl`. */
  lines: string[]
  /** Parsed `<id>.settings.json`, or undefined when absent. */
  settings: unknown
  /** The Task invocation that started this session, when it is a Task subagent. */
  invocation?: TaskInvocation
}

const STATUS_BY_INVOCATION: Record<string, RunStatus> = {
  completed: RUN_STATUSES.COMPLETED,
  running: RUN_STATUSES.RUNNING,
  cancelled: RUN_STATUSES.CANCELLED,
  failed: RUN_STATUSES.FAILED,
  error: RUN_STATUSES.FAILED,
}

const parseLine = (line: string): unknown => {
  try {
    return JSON.parse(line)
  } catch {
    return undefined
  }
}

/** Ids of sessions named in a tool result. The result text itself is dropped right here. */
const sessionIdsIn = (content: unknown): string[] => [...new Set(JSON.stringify(content ?? '').match(SESSION_ID) ?? [])]

function kindOf(settings: DroidSettings | undefined, invocation: TaskInvocation | undefined): AgentKind {
  const mission = settings?.tags.find((t) => t.name === MISSION_TAG)?.metadata?.kind
  if (mission === AGENT_KINDS.ORCHESTRATOR || mission === AGENT_KINDS.AGENT) return mission
  if (invocation || settings?.tags.some((t) => t.name === SUBAGENT_TAG)) return AGENT_KINDS.SUBAGENT
  return AGENT_KINDS.SESSION
}

export function parseTaskInvocations(file: unknown): Map<string, TaskInvocation> {
  const parsed = taskInvocationsFileSchema.safeParse(file)
  const byChild = new Map<string, TaskInvocation>()
  if (!parsed.success) return byChild
  for (const raw of parsed.data.invocations) {
    const inv = taskInvocationSchema.safeParse(raw)
    // a resumed Task reuses its child session; the first invocation is the one that created it
    if (inv.success && !byChild.has(inv.data.childSessionId)) byChild.set(inv.data.childSessionId, inv.data)
  }
  return byChild
}

/** Parses one Droid session into an `AgentRun`. Returns undefined when the file has no usable records. */
export function parseDroidSession(input: DroidSessionInput): AgentRun | undefined {
  const settingsResult = input.settings === undefined ? undefined : settingsSchema.safeParse(input.settings)
  const settings = settingsResult?.success ? settingsResult.data : undefined
  const {invocation} = input

  let callingSessionId: string | undefined
  let startedAt = Number.POSITIVE_INFINITY
  let endedAt = Number.NEGATIVE_INFINITY
  let model: string | undefined
  const turnTimes: number[] = []
  const calls = new Map<string, ToolCall>()

  for (const line of input.lines) {
    const record = parseLine(line)
    const start = sessionStartSchema.safeParse(record)
    if (start.success) {
      callingSessionId = start.data.callingSessionId ?? undefined
      continue
    }
    const any = anyRecordSchema.safeParse(record)
    const at = any.success && any.data.timestamp ? Date.parse(any.data.timestamp) : Number.NaN
    if (!Number.isNaN(at)) {
      startedAt = Math.min(startedAt, at)
      endedAt = Math.max(endedAt, at)
    }
    const msg = messageRecordSchema.safeParse(record)
    if (!msg.success || Number.isNaN(at) || !Array.isArray(msg.data.message.content)) continue
    const {role, modelId, content} = msg.data.message
    if (role === 'assistant') {
      turnTimes.push(at)
      model = modelId ?? model
    }
    for (const raw of content) {
      const block = contentBlockSchema.safeParse(raw)
      if (!block.success) continue
      const b = block.data
      if (role === 'assistant' && 'id' in b && 'name' in b) {
        calls.set(b.id, {id: b.id, name: b.name, startedAt: at, isError: false, spawnedSessionIds: []})
      } else if (role === 'user' && 'tool_use_id' in b) {
        const call = calls.get(b.tool_use_id)
        if (!call) continue
        call.endedAt = at
        call.isError = b.is_error === true
        if (SPAWN_TOOLS.has(call.name)) call.spawnedSessionIds = sessionIdsIn(b.content)
      }
    }
  }

  if (!Number.isFinite(startedAt)) return undefined
  const kind = kindOf(settings, invocation)
  const missionId = settings?.tags.find((t) => t.name === MISSION_TAG)?.metadata?.missionId
  const parentSessionId = invocation?.parentSessionId ?? callingSessionId
  const usage = settings?.tokenUsage

  return {
    harness: HARNESSES.DROID,
    sessionId: input.sessionId,
    kind,
    agentType: invocation?.subagentType,
    workstreamId: kind === AGENT_KINDS.ORCHESTRATOR ? (missionId ?? input.sessionId) : missionId,
    parent: parentSessionId ? {sessionId: parentSessionId, toolCallId: invocation?.parentToolUseId} : undefined,
    status: invocation?.status
      ? (STATUS_BY_INVOCATION[invocation.status] ?? RUN_STATUSES.UNKNOWN)
      : RUN_STATUSES.UNKNOWN,
    startedAt,
    endedAt,
    model: model ?? settings?.model,
    tokens: usage
      ? {
          input: usage.inputTokens,
          output: usage.outputTokens,
          cacheRead: usage.cacheReadTokens,
          cacheWrite: usage.cacheCreationTokens,
        }
      : undefined,
    llmTurns: turnTimes.length,
    turnTimes,
    toolCalls: [...calls.values()].sort((a, b) => a.startedAt - b.startedAt),
  }
}
