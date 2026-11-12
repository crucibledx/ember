import type {Forest, RunNode} from './hierarchy'
import {sessionRootKey, sessionSpanId, toolSpanId, traceIdFor, workstreamRootKey} from './ids'
import {AGENT_KINDS, type AgentRun, RUN_STATUSES, type ToolCall} from './model'

/**
 * OTLP/JSON shapes (opentelemetry-proto, JSON encoding: ids are hex strings, times are nanosecond strings).
 * Hand-written instead of using the OTel SDK because the SDK always mints random ids and cannot export a
 * span whose parent lives in another process; deterministic ids are the point of this exporter.
 */
type AnyValue = {stringValue: string} | {intValue: string} | {boolValue: boolean}
export interface KeyValue {
  key: string
  value: AnyValue
}
export interface OtlpSpanLink {
  traceId: string
  spanId: string
  attributes: KeyValue[]
}
export interface OtlpSpan {
  traceId: string
  spanId: string
  parentSpanId?: string
  name: string
  kind: number
  startTimeUnixNano: string
  endTimeUnixNano: string
  attributes: KeyValue[]
  links?: OtlpSpanLink[]
  status: {code: number; message?: string}
}
export interface OtlpTraceRequest {
  resourceSpans: {
    resource: {attributes: KeyValue[]}
    scopeSpans: {scope: {name: string; version: string}; spans: OtlpSpan[]}[]
  }[]
}

/** Resource `service.name` of everything the session-file exporter sends (spans and logs). */
export const SERVICE_NAME = 'ember-backfill'

const SPAN_KIND_INTERNAL = 1
const STATUS_UNSET = 0
const STATUS_ERROR = 2

/**
 * Every attribute key an exported span may carry. Anything else never leaves the process: prompts, tool
 * inputs and results, titles and paths are not even present on `AgentRun`, and this list keeps it that way
 * if they are ever added.
 */
export const EXPORTED_ATTRIBUTES = [
  'gen_ai.operation.name',
  'gen_ai.agent.name',
  'gen_ai.agent.id',
  'gen_ai.conversation.id',
  'gen_ai.request.model',
  'gen_ai.usage.input_tokens',
  'gen_ai.usage.output_tokens',
  'gen_ai.tool.name',
  'gen_ai.tool.call.id',
  'session.id',
  'error.type',
  'ember.harness',
  'ember.agent.kind',
  'ember.agent.status',
  'ember.agent.llm_turns',
  'ember.agent.tool_calls',
  'ember.agent.children',
  'ember.workstream.id',
  'ember.parent.session.id',
  'ember.parent.tool_call.id',
  'ember.usage.cache_read_tokens',
  'ember.usage.cache_write_tokens',
  'ember.tool.incomplete',
  'ember.native.traces',
] as const
type AttributeKey = (typeof EXPORTED_ATTRIBUTES)[number]
type Scalar = string | number | boolean | undefined
type Attributes = Partial<Record<AttributeKey, Scalar>>

export const toKeyValues = <K extends string>(attrs: Partial<Record<K, Scalar>>): KeyValue[] =>
  Object.entries(attrs)
    .filter((entry): entry is [string, string | number | boolean] => entry[1] !== undefined)
    .map(([key, v]) => ({
      key,
      value:
        typeof v === 'number'
          ? {intValue: String(Math.round(v))}
          : typeof v === 'boolean'
            ? {boolValue: v}
            : {stringValue: v},
    }))

export const nanos = (ms: number): string => (BigInt(Math.round(ms)) * 1_000_000n).toString()

export const traceIdOf = (node: RunNode): string =>
  traceIdFor(
    node.rootKey.type === 'workstream'
      ? workstreamRootKey(node.rootKey.id)
      : sessionRootKey(node.run.harness, node.rootKey.id),
  )

function parentSpanIdOf(node: RunNode): string | undefined {
  const {parent, run} = node
  if (!parent) return undefined
  return parent.toolCallId ? toolSpanId(run.harness, parent.toolCallId) : sessionSpanId(run.harness, parent.sessionId)
}

const agentName = (run: AgentRun): string => run.agentType ?? run.kind

function sessionSpan(node: RunNode, traceId: string): OtlpSpan {
  const {run} = node
  const failed = run.status === RUN_STATUSES.FAILED
  return {
    traceId,
    spanId: sessionSpanId(run.harness, run.sessionId),
    parentSpanId: parentSpanIdOf(node),
    name: `invoke_agent ${agentName(run)}`,
    kind: SPAN_KIND_INTERNAL,
    startTimeUnixNano: nanos(run.startedAt),
    endTimeUnixNano: nanos(Math.max(run.endedAt, run.startedAt)),
    attributes: toKeyValues({
      'gen_ai.operation.name': 'invoke_agent',
      'gen_ai.agent.name': agentName(run),
      'gen_ai.agent.id': run.sessionId,
      'gen_ai.conversation.id': run.sessionId,
      'session.id': run.sessionId,
      'gen_ai.request.model': run.model,
      'gen_ai.usage.input_tokens': run.tokens?.input,
      'gen_ai.usage.output_tokens': run.tokens?.output,
      'ember.usage.cache_read_tokens': run.tokens?.cacheRead,
      'ember.usage.cache_write_tokens': run.tokens?.cacheWrite,
      'ember.harness': run.harness,
      'ember.agent.kind': run.kind,
      'ember.agent.status': run.status,
      'ember.agent.llm_turns': run.llmTurns,
      'ember.agent.tool_calls': run.toolCalls.length,
      'ember.agent.children': node.children.length,
      'ember.workstream.id': node.workstreamId,
      'ember.parent.session.id': node.parent?.sessionId,
      'ember.parent.tool_call.id': node.parent?.toolCallId,
      'error.type': failed ? 'agent_failed' : undefined,
    }),
    status: failed ? {code: STATUS_ERROR, message: 'agent failed'} : {code: STATUS_UNSET},
  }
}

function toolSpan(run: AgentRun, call: ToolCall, traceId: string): OtlpSpan {
  return {
    traceId,
    spanId: toolSpanId(run.harness, call.id),
    parentSpanId: sessionSpanId(run.harness, run.sessionId),
    name: `execute_tool ${call.name}`,
    kind: SPAN_KIND_INTERNAL,
    startTimeUnixNano: nanos(call.startedAt),
    endTimeUnixNano: nanos(Math.max(call.endedAt ?? run.endedAt, call.startedAt)),
    attributes: toKeyValues({
      'gen_ai.operation.name': 'execute_tool',
      'gen_ai.tool.name': call.name,
      'gen_ai.tool.call.id': call.id,
      'session.id': run.sessionId,
      'ember.harness': run.harness,
      'ember.tool.incomplete': call.endedAt === undefined ? true : undefined,
      'error.type': call.isError ? 'tool_error' : undefined,
    }),
    status: call.isError ? {code: STATUS_ERROR, message: 'tool reported an error'} : {code: STATUS_UNSET},
  }
}

/** A harness's own trace for one turn of a session, found in the backend (Droid `invoke_agent droid` root). */
export interface NativeTurn {
  traceId: string
  spanId: string
}

const attrValue = (span: OtlpSpan, key: string): string | undefined => {
  const v = span.attributes.find((a) => a.key === key)?.value
  return v && 'stringValue' in v ? v.stringValue : undefined
}

/**
 * Sessions whose harness already exported native spans keep the native tool spans: the Ember tool spans for
 * them are dropped (no duplicates), and the Ember session span links to every native turn trace instead.
 */
export function linkNativeTurns(spans: OtlpSpan[], native: Map<string, NativeTurn[]>): OtlpSpan[] {
  return spans.flatMap((span) => {
    const turns = native.get(attrValue(span, 'session.id') ?? '')
    if (!turns?.length) return [span]
    if (span.name.startsWith('execute_tool')) return []
    return [
      {
        ...span,
        attributes: [...span.attributes, ...toKeyValues<AttributeKey>({'ember.native.traces': turns.length})],
        links: turns.map((t) => ({
          traceId: t.traceId,
          spanId: t.spanId,
          attributes: [{key: 'ember.link.type', value: {stringValue: 'native_turn'}}],
        })),
      },
    ]
  })
}

export function spansOf(node: RunNode): OtlpSpan[] {
  const traceId = traceIdOf(node)
  return [sessionSpan(node, traceId), ...node.run.toolCalls.map((call) => toolSpan(node.run, call, traceId))]
}

export function forestSpans(forest: Forest): OtlpSpan[] {
  return [...forest.nodes.values()].flatMap(spansOf)
}

export function toTraceRequest(spans: OtlpSpan[], version: string): OtlpTraceRequest {
  return {
    resourceSpans: [
      {
        resource: {
          attributes: toKeyValues({}).concat([
            {key: 'service.name', value: {stringValue: SERVICE_NAME}},
            {key: 'service.version', value: {stringValue: version}},
          ]),
        },
        scopeSpans: [{scope: {name: 'ember.session-files', version}, spans}],
      },
    ],
  }
}

export const isWorkstreamRoot = (node: RunNode): boolean => node.run.kind === AGENT_KINDS.ORCHESTRATOR
