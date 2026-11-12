import type {Forest, RunNode} from './hierarchy'
import {sessionSpanId, toolSpanId} from './ids'
import {type AgentRun, RUN_STATUSES, type ToolCall} from './model'
import {type KeyValue, nanos, toKeyValues, traceIdOf} from './otlp'

/** OTLP/JSON log record. traceId/spanId are the record's own fields, which is what backends join on. */
export interface OtlpLogRecord {
  timeUnixNano: string
  observedTimeUnixNano: string
  severityNumber: number
  severityText: string
  body: {stringValue: string}
  attributes: KeyValue[]
  traceId: string
  spanId: string
}
export interface OtlpLogsRequest {
  resourceLogs: {
    resource: {attributes: KeyValue[]}
    scopeLogs: {scope: {name: string; version: string}; logRecords: OtlpLogRecord[]}[]
  }[]
}

export const LOG_EVENTS = {
  AGENT_STARTED: 'ember.agent.started',
  LLM_TURN: 'ember.llm.turn',
  TOOL_CALL: 'ember.tool.call',
  AGENT_ENDED: 'ember.agent.ended',
} as const

const SEVERITY = {INFO: {number: 9, text: 'INFO'}, ERROR: {number: 17, text: 'ERROR'}} as const

/**
 * Every attribute key a log record may carry. Bodies are built only from names, enums and numbers below, so
 * no prompt, tool input or result can reach a log even if the parser starts keeping one.
 */
export const EXPORTED_LOG_ATTRIBUTES = [
  'event.name',
  'session.id',
  'gen_ai.agent.name',
  'gen_ai.request.model',
  'gen_ai.tool.name',
  'gen_ai.tool.call.id',
  'error.type',
  'ember.harness',
  'ember.agent.kind',
  'ember.agent.status',
  'ember.workstream.id',
  'ember.tool.duration_ms',
  'ember.tool.incomplete',
  'ember.llm.turn',
] as const
type LogKey = (typeof EXPORTED_LOG_ATTRIBUTES)[number]

const agentName = (run: AgentRun): string => run.agentType ?? run.kind

function record(
  at: number,
  severity: (typeof SEVERITY)[keyof typeof SEVERITY],
  body: string,
  traceId: string,
  spanId: string,
  attrs: Partial<Record<LogKey, string | number | boolean | undefined>>,
): OtlpLogRecord {
  return {
    timeUnixNano: nanos(at),
    observedTimeUnixNano: nanos(at),
    severityNumber: severity.number,
    severityText: severity.text,
    body: {stringValue: body},
    attributes: toKeyValues<LogKey>(attrs),
    traceId,
    spanId,
  }
}

function toolRecord(node: RunNode, call: ToolCall, traceId: string, spanId: string): OtlpLogRecord {
  const {run} = node
  const done = call.endedAt !== undefined
  const ms = done ? call.endedAt! - call.startedAt : undefined
  const outcome = call.isError ? 'error' : done ? 'ok' : 'incomplete'
  return record(
    call.endedAt ?? call.startedAt,
    call.isError ? SEVERITY.ERROR : SEVERITY.INFO,
    `tool ${call.name} ${outcome}${ms === undefined ? '' : ` ${ms}ms`}`,
    traceId,
    spanId,
    {
      'event.name': LOG_EVENTS.TOOL_CALL,
      'session.id': run.sessionId,
      'gen_ai.agent.name': agentName(run),
      'gen_ai.tool.name': call.name,
      'gen_ai.tool.call.id': call.id,
      'error.type': call.isError ? 'tool_error' : undefined,
      'ember.harness': run.harness,
      'ember.agent.kind': run.kind,
      'ember.workstream.id': node.workstreamId,
      'ember.tool.duration_ms': ms,
      'ember.tool.incomplete': done ? undefined : true,
    },
  )
}

/**
 * One record per agent start and end, model turn and tool call, each pointing at the span it belongs to.
 * Sessions in `nativeSessions` have no Ember tool spans (the harness's own spans stand in), so their tool
 * records point at the session span.
 */
export function logsOf(node: RunNode, nativeSessions: ReadonlySet<string> = new Set()): OtlpLogRecord[] {
  const {run} = node
  const traceId = traceIdOf(node)
  const sessionSpan = sessionSpanId(run.harness, run.sessionId)
  const failed = run.status === RUN_STATUSES.FAILED
  const common = {
    'session.id': run.sessionId,
    'gen_ai.agent.name': agentName(run),
    'ember.harness': run.harness,
    'ember.agent.kind': run.kind,
    'ember.workstream.id': node.workstreamId,
  }
  const linked = nativeSessions.has(run.sessionId)
  return [
    record(run.startedAt, SEVERITY.INFO, `agent ${agentName(run)} started (${run.kind})`, traceId, sessionSpan, {
      ...common,
      'event.name': LOG_EVENTS.AGENT_STARTED,
      'gen_ai.request.model': run.model,
    }),
    ...run.turnTimes.map((at, i) =>
      record(at, SEVERITY.INFO, `llm turn ${i + 1}`, traceId, sessionSpan, {
        ...common,
        'event.name': LOG_EVENTS.LLM_TURN,
        'gen_ai.request.model': run.model,
        'ember.llm.turn': i + 1,
      }),
    ),
    ...run.toolCalls.map((call) =>
      toolRecord(node, call, traceId, linked ? sessionSpan : toolSpanId(run.harness, call.id)),
    ),
    record(
      Math.max(run.endedAt, run.startedAt),
      failed ? SEVERITY.ERROR : SEVERITY.INFO,
      `agent ${agentName(run)} ended: ${run.status}, ${run.toolCalls.length} tool calls, ${run.llmTurns} llm turns`,
      traceId,
      sessionSpan,
      {
        ...common,
        'event.name': LOG_EVENTS.AGENT_ENDED,
        'ember.agent.status': run.status,
        'error.type': failed ? 'agent_failed' : undefined,
      },
    ),
  ]
}

export const forestLogs = (forest: Forest, nativeSessions?: ReadonlySet<string>): OtlpLogRecord[] =>
  [...forest.nodes.values()].flatMap((n) => logsOf(n, nativeSessions))

const sessionOf = (r: OtlpLogRecord): string => {
  const v = r.attributes.find((a) => a.key === 'session.id')?.value
  return v && 'stringValue' in v ? v.stringValue : ''
}

/**
 * Records are sorted by time and grouped per session, with the session as `service.instance.id`. Loki turns
 * that into one stream per session and rejects entries far older than the newest one already in a stream, so a
 * backfill that mixed sessions in one stream (or sent them out of order) would lose most of its records.
 */
export function toLogsRequest(records: OtlpLogRecord[], service: {name: string; version: string}): OtlpLogsRequest {
  const bySession = new Map<string, OtlpLogRecord[]>()
  for (const r of [...records].sort((a, b) => (BigInt(a.timeUnixNano) < BigInt(b.timeUnixNano) ? -1 : 1))) {
    bySession.set(sessionOf(r), [...(bySession.get(sessionOf(r)) ?? []), r])
  }
  return {
    resourceLogs: [...bySession].map(([sessionId, logRecords]) => ({
      resource: {
        attributes: [
          {key: 'service.name', value: {stringValue: service.name}},
          {key: 'service.version', value: {stringValue: service.version}},
          {key: 'service.instance.id', value: {stringValue: sessionId}},
        ],
      },
      scopeLogs: [{scope: {name: 'ember.session-files', version: service.version}, logRecords}],
    })),
  }
}
