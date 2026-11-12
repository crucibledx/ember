import type {NativeTurn} from '@domain/otlp'

interface SearchSpan {
  spanID: string
  attributes?: {key: string; value: {stringValue?: string}}[]
}
interface SearchResponse {
  traces?: {traceID: string; spanSets?: {spans?: SearchSpan[]}[]}[]
}

/**
 * Finds Droid's own turn traces (root span `invoke_agent droid`, service `cli`) in Tempo, per session id.
 * Read-only TraceQL search; `limit` bounds the result, so a very busy range can miss turns (they then simply
 * stay unlinked).
 */
export async function findNativeTurns(
  tempo: string,
  fromMs: number,
  toMs: number,
  limit = 5000,
): Promise<Map<string, NativeTurn[]>> {
  const url = new URL('/api/search', tempo)
  url.searchParams.set(
    'q',
    '{ resource.service.name = "cli" && span.gen_ai.operation.name = "invoke_agent" } | select(span.session.id)',
  )
  url.searchParams.set('start', String(Math.floor(fromMs / 1000) - 60))
  url.searchParams.set('end', String(Math.ceil(toMs / 1000) + 60))
  url.searchParams.set('limit', String(limit))
  url.searchParams.set('spss', '1')
  const res = await fetch(url)
  if (!res.ok) throw new Error(`Tempo search at ${url.origin} failed: HTTP ${res.status} ${await res.text()}`)
  const body = (await res.json()) as SearchResponse
  const bySession = new Map<string, NativeTurn[]>()
  for (const trace of body.traces ?? []) {
    for (const span of trace.spanSets?.flatMap((s) => s.spans ?? []) ?? []) {
      const sessionId = span.attributes?.find((a) => a.key === 'session.id')?.value.stringValue
      if (!sessionId) continue
      // Tempo's search API drops leading zeros from ids; OTLP needs the full 32/16 hex characters.
      const turn = {traceId: trace.traceID.padStart(32, '0'), spanId: span.spanID.padStart(16, '0')}
      bySession.set(sessionId, [...(bySession.get(sessionId) ?? []), turn])
    }
  }
  return bySession
}
