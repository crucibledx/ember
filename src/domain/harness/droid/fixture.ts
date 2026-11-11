import type {DroidSessionInput} from './parse'

/** Synthetic Droid session files in the shape found on disk. No real session data. */
export const SECRET = 'TOP-SECRET-PROMPT-TEXT'
export const IDS = {
  orchestrator: '00000000-0000-4000-8000-000000000001',
  agent: '00000000-0000-4000-8000-000000000002',
  subagent: '00000000-0000-4000-8000-000000000003',
  spawnCall: 'toolu_spawn',
  taskCall: 'toolu_task',
  readCall: 'toolu_read',
} as const

const t = (min: number): string => new Date(Date.UTC(2026, 10, 11, 10, min)).toISOString()
const line = (record: unknown): string => JSON.stringify(record)
const start = (id: string, caller?: string): string =>
  line({type: 'session_start', id, title: SECRET, cwd: '/secret/path', callingSessionId: caller})
const msg = (at: number, role: string, content: unknown[]): string =>
  line({type: 'message', id: `m${at}${role}`, timestamp: t(at), message: {role, content, modelId: 'model-x'}})

export const orchestratorInput: DroidSessionInput = {
  sessionId: IDS.orchestrator,
  settings: {tags: [{name: 'mission-v2', metadata: {kind: 'orchestrator', missionId: IDS.orchestrator}}]},
  lines: [
    start(IDS.orchestrator),
    msg(0, 'user', [{type: 'text', text: SECRET}]),
    msg(1, 'assistant', [{type: 'tool_use', id: IDS.spawnCall, name: 'SpawnAgent', input: {prompt: SECRET}}]),
    msg(2, 'user', [{type: 'tool_result', tool_use_id: IDS.spawnCall, content: `spawned ${IDS.agent} ${SECRET}`}]),
  ],
}

export const agentInput: DroidSessionInput = {
  sessionId: IDS.agent,
  settings: {
    tags: [{name: 'mission-v2', metadata: {kind: 'agent', missionId: IDS.orchestrator}}],
    tokenUsage: {inputTokens: 10, outputTokens: 20, cacheReadTokens: 30, cacheCreationTokens: 40},
  },
  lines: [
    start(IDS.agent, IDS.orchestrator),
    msg(2, 'user', [{type: 'text', text: SECRET}]),
    msg(3, 'assistant', [
      {type: 'tool_use', id: IDS.readCall, name: 'Read', input: {path: SECRET}},
      {type: 'tool_use', id: IDS.taskCall, name: 'Task', input: {prompt: SECRET, subagent_type: 'worker'}},
    ]),
    msg(4, 'user', [{type: 'tool_result', tool_use_id: IDS.readCall, is_error: true, content: SECRET}]),
    msg(9, 'user', [{type: 'tool_result', tool_use_id: IDS.taskCall, content: SECRET}]),
    'not json at all',
  ],
}

export const subagentInput: DroidSessionInput = {
  sessionId: IDS.subagent,
  settings: {tags: [{name: 'subagent'}]},
  invocation: {
    childSessionId: IDS.subagent,
    parentSessionId: IDS.agent,
    parentToolUseId: IDS.taskCall,
    subagentType: 'worker',
    status: 'completed',
  },
  lines: [
    start(IDS.subagent, IDS.agent),
    msg(5, 'user', [{type: 'text', text: SECRET}]),
    msg(8, 'assistant', [{type: 'text', text: SECRET}]),
  ],
}
