import {describe, expect, it} from 'bun:test'

import {agentInput, IDS, orchestratorInput, SECRET, subagentInput} from './fixture'
import {parseDroidSession, parseTaskInvocations} from './parse'

describe('parseDroidSession', () => {
  it('reads kind, workstream, tokens and tool call timing from an agent session', () => {
    const run = parseDroidSession(agentInput)!
    expect(run.kind).toBe('agent')
    expect(run.workstreamId).toBe(IDS.orchestrator)
    expect(run.parent).toEqual({sessionId: IDS.orchestrator, toolCallId: undefined})
    expect(run.tokens).toEqual({input: 10, output: 20, cacheRead: 30, cacheWrite: 40})
    expect(run.toolCalls.map((c) => [c.name, c.isError, c.endedAt! - c.startedAt])).toEqual([
      ['Read', true, 60_000],
      ['Task', false, 360_000],
    ])
    expect(run.llmTurns).toBe(1)
  })

  it('links a Task subagent to the exact parent tool call', () => {
    const run = parseDroidSession(subagentInput)!
    expect(run.kind).toBe('subagent')
    expect(run.agentType).toBe('worker')
    expect(run.status).toBe('completed')
    expect(run.parent).toEqual({sessionId: IDS.agent, toolCallId: IDS.taskCall})
  })

  it('keeps only the session id from a SpawnAgent result', () => {
    const run = parseDroidSession(orchestratorInput)!
    expect(run.kind).toBe('orchestrator')
    expect(run.workstreamId).toBe(IDS.orchestrator)
    expect(run.toolCalls[0]!.spawnedSessionIds).toEqual([IDS.agent])
  })

  it('never carries prompt, title, path or tool content', () => {
    for (const input of [orchestratorInput, agentInput, subagentInput]) {
      const serialized = JSON.stringify(parseDroidSession(input))
      expect(serialized).not.toContain(SECRET)
      expect(serialized).not.toContain('/secret/path')
    }
  })

  it('returns undefined for a file with no timestamped records', () => {
    expect(parseDroidSession({sessionId: 'x', lines: ['{"type":"session_start","id":"x"}'], settings: undefined})).toBe(
      undefined,
    )
  })
})

describe('parseTaskInvocations', () => {
  it('keeps the first invocation of a resumed child and skips malformed rows', () => {
    const map = parseTaskInvocations({
      invocations: [
        {childSessionId: 'c', parentSessionId: 'p', parentToolUseId: 'first'},
        {childSessionId: 'c', parentSessionId: 'p', parentToolUseId: 'resume'},
        {nonsense: true},
      ],
    })
    expect(map.size).toBe(1)
    expect(map.get('c')!.parentToolUseId).toBe('first')
  })
})
