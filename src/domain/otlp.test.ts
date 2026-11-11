import {describe, expect, it} from 'bun:test'

import {agentInput, IDS, orchestratorInput, SECRET, subagentInput} from './harness/droid/fixture'
import {parseDroidSession} from './harness/droid/parse'
import {buildForest} from './hierarchy'
import {sessionSpanId, toolSpanId} from './ids'
import type {AgentRun} from './model'
import {EXPORTED_ATTRIBUTES, forestSpans, type OtlpSpan} from './otlp'

const runs = (...inputs: (typeof agentInput)[]): AgentRun[] => inputs.map((i) => parseDroidSession(i)!)
const byId = (spans: OtlpSpan[]): Map<string, OtlpSpan> => new Map(spans.map((s) => [s.spanId, s]))

describe('forestSpans', () => {
  const all = forestSpans(buildForest(runs(orchestratorInput, agentInput, subagentInput)))
  const spans = byId(all)

  it('puts a whole workstream in one trace', () => {
    expect(new Set(all.map((s) => s.traceId)).size).toBe(1)
  })

  it('parents agent -> spawn call, subagent -> Task call, tool -> session', () => {
    const agent = spans.get(sessionSpanId('droid', IDS.agent))!
    const sub = spans.get(sessionSpanId('droid', IDS.subagent))!
    const read = spans.get(toolSpanId('droid', IDS.readCall))!
    expect(agent.parentSpanId).toBe(toolSpanId('droid', IDS.spawnCall))
    expect(sub.parentSpanId).toBe(toolSpanId('droid', IDS.taskCall))
    expect(read.parentSpanId).toBe(sessionSpanId('droid', IDS.agent))
    expect(read.status.code).toBe(2)
    expect(sub.name).toBe('invoke_agent worker')
  })

  it('produces the same trace when orchestrator and agent are scanned on different machines', () => {
    // machine B (agent + its Task subagent) exports before machine A (orchestrator)
    const parts = [
      ...forestSpans(buildForest(runs(subagentInput, agentInput))),
      ...forestSpans(buildForest(runs(orchestratorInput))),
    ]
    const split = byId(parts)
    // the only difference allowed: an agent scanned alone cannot see which spawn call started it
    const agentId = sessionSpanId('droid', IDS.agent)
    expect(split.get(agentId)!.parentSpanId).toBe(sessionSpanId('droid', IDS.orchestrator))
    for (const [id, span] of spans) {
      if (id === agentId) continue
      const other = split.get(id)!
      expect([other.traceId, other.parentSpanId, other.name]).toEqual([span.traceId, span.parentSpanId, span.name])
    }
  })

  it('keeps the parent span of a subagent scanned without its parent, but cannot place it in the workstream', () => {
    // Droid records the workstream only on orchestrator and agent sessions; a Task subagent learns it from its
    // parent, which always runs on the same machine. Known limit: alone, it lands in a trace of its own.
    const alone = forestSpans(buildForest(runs(subagentInput)))[0]!
    const full = spans.get(sessionSpanId('droid', IDS.subagent))!
    expect(alone.parentSpanId).toBe(full.parentSpanId)
    expect(alone.traceId).not.toBe(full.traceId)
  })

  it('is deterministic across runs', () => {
    const again = forestSpans(buildForest(runs(orchestratorInput, agentInput, subagentInput)))
    expect(again).toEqual(all)
  })

  it('exports only allowlisted attributes and no content', () => {
    const allowed = new Set<string>(EXPORTED_ATTRIBUTES)
    for (const span of all) for (const a of span.attributes) expect(allowed.has(a.key)).toBe(true)
    expect(JSON.stringify(all)).not.toContain(SECRET)
  })
})
