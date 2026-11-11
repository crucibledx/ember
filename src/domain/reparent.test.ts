import {describe, expect, it} from 'bun:test'

import {sessionSpanId, toolSpanId} from './ids'
import type {KeyValue} from './otlp'
import {reparentHarnessSpans, type SessionPlacement} from './reparent'

const str = (key: string, value: string): KeyValue => ({key, value: {stringValue: value}})
const span = (spanId: string, name: string, parentSpanId: string | undefined, attrs: KeyValue[]) => ({
  traceId: 'native-trace',
  spanId,
  ...(parentSpanId ? {parentSpanId} : {}),
  name,
  attributes: attrs,
  startTimeUnixNano: '1',
})
const request = (service: string, spans: ReturnType<typeof span>[]) => ({
  resourceSpans: [{resource: {attributes: [str('service.name', service)]}, scopeSpans: [{spans}]}],
})

const SESSION = 'session-1'
const PLACEMENT: SessionPlacement = {traceId: 'ember-trace', sessionSpanId: sessionSpanId('droid', SESSION)}
const resolve = (id: string) => (id === SESSION ? PLACEMENT : undefined)

const turn = () => [
  span('root', 'invoke_agent droid', undefined, [str('session.id', SESSION)]),
  span('chat', 'chat model', 'root', [str('session.id', SESSION)]),
  span('task', 'execute_tool Task', 'root', [str('session.id', SESSION), str('gen_ai.tool.call.id', 'call-1')]),
]

describe('reparentHarnessSpans', () => {
  it('moves a Droid turn under the Ember session span and gives tool spans their Ember id', () => {
    const {request: out, stats} = reparentHarnessSpans(request('cli', turn()), resolve)
    const [root, chat, task] = out.resourceSpans[0]!.scopeSpans[0]!.spans
    expect(stats).toEqual({spans: 3, reparented: 3, unresolvedSessions: []})
    expect(new Set([root, chat, task].map((s) => s!.traceId))).toEqual(new Set(['ember-trace']))
    expect(root!.parentSpanId).toBe(PLACEMENT.sessionSpanId)
    expect(chat!.parentSpanId).toBe('root')
    // a subagent's Ember span points at toolSpanId(<Task call id>): it now lands under Droid's own Task span
    expect(task!.spanId).toBe(toolSpanId('droid', 'call-1'))
    expect(task!.parentSpanId).toBe('root')
    expect(root!.attributes).toContainEqual(str('ember.original_trace_id', 'native-trace'))
  })

  it('remaps references to a renamed tool span inside the batch', () => {
    const spans = [...turn(), span('child', 'inner', 'task', [str('session.id', SESSION)])]
    const out = reparentHarnessSpans(request('cli', spans), resolve).request.resourceSpans[0]!.scopeSpans[0]!.spans
    expect(out[3]!.parentSpanId).toBe(toolSpanId('droid', 'call-1'))
  })

  it('passes unknown sessions and other services through unchanged and reports them', () => {
    const unknown = turn().map((s) => ({...s, attributes: [str('session.id', 'other')]}))
    const a = reparentHarnessSpans(request('cli', unknown), resolve)
    expect(a.stats.unresolvedSessions).toEqual(['other'])
    expect(a.request.resourceSpans[0]!.scopeSpans[0]!.spans).toEqual(
      turn().map((s) => ({...s, attributes: [str('session.id', 'other')]})),
    )
    const b = reparentHarnessSpans(request('claude-code', turn()), resolve)
    expect(b.stats.reparented).toBe(0)
    expect(b.request.resourceSpans[0]!.scopeSpans[0]!.spans).toEqual(turn())
  })
})
