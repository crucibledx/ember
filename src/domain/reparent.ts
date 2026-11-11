import {toolSpanId} from './ids'
import type {Harness} from './model'
import type {KeyValue} from './otlp'

/**
 * Where a harness session sits in the Ember tree: the trace it belongs to and the span that stands for it.
 * Both are deterministic, so the relay and the skeleton exporter agree without talking to each other.
 */
export interface SessionPlacement {
  traceId: string
  sessionSpanId: string
}
export type ResolveSession = (sessionId: string) => SessionPlacement | undefined

/** Loose OTLP/JSON span: everything the relay does not touch is carried through untouched. */
interface LooseSpan {
  traceId: string
  spanId: string
  parentSpanId?: string
  name: string
  attributes?: KeyValue[]
  [k: string]: unknown
}
interface LooseRequest {
  resourceSpans?: {
    resource?: {attributes?: KeyValue[]}
    scopeSpans?: {spans?: LooseSpan[]; [k: string]: unknown}[]
    [k: string]: unknown
  }[]
}

export interface ReparentStats {
  spans: number
  reparented: number
  unresolvedSessions: string[]
}

/** Which resources belong to which harness. Droid documents `service.name` = `cli` for its spans. */
const HARNESS_BY_SERVICE: Record<string, Harness> = {cli: 'droid'}

const attr = (attrs: KeyValue[] | undefined, key: string): string | undefined => {
  const v = attrs?.find((a) => a.key === key)?.value
  return v && 'stringValue' in v ? v.stringValue : undefined
}

/**
 * Rewrites a harness's own spans so they hang under the Ember tree instead of forming separate roots:
 * - every span of a session's trace moves to the session's Ember trace;
 * - the harness root span (no parent) becomes a child of the Ember session span;
 * - `execute_tool` spans take the deterministic tool span id, so a subagent's Ember span, which points at
 *   `toolSpanId(<Task call id>)`, lands under the harness's own Task span. Droid documents tool spans as
 *   leaves (children of the turn root), so no other span refers to the old id; references inside the batch
 *   are remapped anyway.
 * Spans of unknown sessions or other services pass through unchanged: the relay never drops data.
 */
export function reparentHarnessSpans<T extends LooseRequest>(
  request: T,
  resolve: ResolveSession,
): {request: T; stats: ReparentStats} {
  const stats: ReparentStats = {spans: 0, reparented: 0, unresolvedSessions: []}
  for (const rs of request.resourceSpans ?? []) {
    const harness = HARNESS_BY_SERVICE[attr(rs.resource?.attributes, 'service.name') ?? '']
    const spans = (rs.scopeSpans ?? []).flatMap((ss) => ss.spans ?? [])
    stats.spans += spans.length
    if (!harness) continue

    const byTrace = new Map<string, LooseSpan[]>()
    for (const span of spans) byTrace.set(span.traceId, [...(byTrace.get(span.traceId) ?? []), span])

    for (const [originalTraceId, traceSpans] of byTrace) {
      const sessionId = traceSpans.map((s) => attr(s.attributes, 'session.id')).find(Boolean)
      const placement = sessionId ? resolve(sessionId) : undefined
      if (!sessionId || !placement) {
        if (sessionId && !stats.unresolvedSessions.includes(sessionId)) stats.unresolvedSessions.push(sessionId)
        continue
      }
      const newIds = new Map<string, string>()
      for (const span of traceSpans) {
        const callId = attr(span.attributes, 'gen_ai.tool.call.id')
        if (span.name.startsWith('execute_tool') && callId) newIds.set(span.spanId, toolSpanId(harness, callId))
      }
      for (const span of traceSpans) {
        span.traceId = placement.traceId
        span.spanId = newIds.get(span.spanId) ?? span.spanId
        span.parentSpanId = span.parentSpanId
          ? (newIds.get(span.parentSpanId) ?? span.parentSpanId)
          : placement.sessionSpanId
        span.attributes = [
          ...(span.attributes ?? []),
          {key: 'ember.original_trace_id', value: {stringValue: originalTraceId}},
        ]
        stats.reparented += 1
      }
    }
  }
  return {request, stats}
}
