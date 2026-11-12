import {describe, expect, it} from 'bun:test'

import {agentInput, IDS, orchestratorInput, SECRET, subagentInput} from './harness/droid/fixture'
import {parseDroidSession} from './harness/droid/parse'
import {buildForest} from './hierarchy'
import {sessionSpanId, toolSpanId} from './ids'
import {EXPORTED_LOG_ATTRIBUTES, forestLogs, LOG_EVENTS, type OtlpLogRecord, toLogsRequest} from './logs'
import {forestSpans, linkNativeTurns} from './otlp'

const forest = buildForest([orchestratorInput, agentInput, subagentInput].map((i) => parseDroidSession(i)!))
const spans = forestSpans(forest)
const logs = forestLogs(forest)
const attr = (r: OtlpLogRecord, key: string): unknown => {
  const v = r.attributes.find((a) => a.key === key)?.value
  return v && Object.values(v)[0]
}

describe('forestLogs', () => {
  it('writes one record per agent start and end, model turn and tool call', () => {
    const count = (event: string): number => logs.filter((r) => attr(r, 'event.name') === event).length
    expect(count(LOG_EVENTS.AGENT_STARTED)).toBe(3)
    expect(count(LOG_EVENTS.AGENT_ENDED)).toBe(3)
    expect(count(LOG_EVENTS.LLM_TURN)).toBe(3)
    expect(count(LOG_EVENTS.TOOL_CALL)).toBe(3)
  })

  it('puts every record on an exported span of the same trace', () => {
    const exported = new Set(spans.map((s) => `${s.traceId}/${s.spanId}`))
    for (const r of logs) expect(exported.has(`${r.traceId}/${r.spanId}`)).toBe(true)
  })

  it('logs a failed tool call at ERROR on the tool span', () => {
    const read = logs.find((r) => attr(r, 'gen_ai.tool.call.id') === IDS.readCall)!
    expect(read.spanId).toBe(toolSpanId('droid', IDS.readCall))
    expect([read.severityText, read.body.stringValue, attr(read, 'error.type')]).toEqual([
      'ERROR',
      'tool Read error 60000ms',
      'tool_error',
    ])
  })

  it('exports only allowlisted attributes and no content (planted secret)', () => {
    const allowed = new Set<string>(EXPORTED_LOG_ATTRIBUTES)
    for (const r of logs) for (const a of r.attributes) expect(allowed.has(a.key)).toBe(true)
    const serialized = JSON.stringify(logs)
    expect(serialized).not.toContain(SECRET)
    expect(serialized).not.toContain('/secret/path')
  })

  it('is deterministic across runs', () => {
    expect(forestLogs(forest)).toEqual(logs)
  })

  it('sends one resource per session, records in time order', () => {
    const request = toLogsRequest(logs, {name: 'ember-backfill', version: 't'})
    expect(request.resourceLogs.length).toBe(3)
    for (const rl of request.resourceLogs) {
      const times = rl.scopeLogs[0]!.logRecords.map((r) => BigInt(r.timeUnixNano))
      expect(times).toEqual([...times].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0)))
    }
  })
})

describe('linkNativeTurns', () => {
  const native = new Map([[IDS.agent, [{traceId: 'f'.repeat(32), spanId: 'e'.repeat(16)}]]])
  const linked = linkNativeTurns(spans, native)

  it('drops Ember tool spans of a session the harness traced itself and links its session span instead', () => {
    expect(linked.some((s) => s.spanId === toolSpanId('droid', IDS.readCall))).toBe(false)
    expect(linked.some((s) => s.spanId === toolSpanId('droid', IDS.spawnCall))).toBe(true)
    const agent = linked.find((s) => s.spanId === sessionSpanId('droid', IDS.agent))!
    expect(agent.links).toEqual([
      {
        traceId: 'f'.repeat(32),
        spanId: 'e'.repeat(16),
        attributes: [{key: 'ember.link.type', value: {stringValue: 'native_turn'}}],
      },
    ])
  })

  it('moves tool records of a linked session onto its session span', () => {
    const records = forestLogs(forest, new Set([IDS.agent]))
    const read = records.find((r) => attr(r, 'gen_ai.tool.call.id') === IDS.readCall)!
    expect(read.spanId).toBe(sessionSpanId('droid', IDS.agent))
    const exported = new Set(linked.map((s) => `${s.traceId}/${s.spanId}`))
    for (const r of records) expect(exported.has(`${r.traceId}/${r.spanId}`)).toBe(true)
  })
})
