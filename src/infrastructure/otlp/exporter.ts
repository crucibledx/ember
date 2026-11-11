import type {OtlpSpan, OtlpTraceRequest} from '@domain/otlp'
import {toTraceRequest} from '@domain/otlp'

const BATCH = 500

/** POSTs OTLP/JSON to `<endpoint>/v1/traces`, in batches. */
export async function exportSpans(endpoint: string, spans: OtlpSpan[], version: string): Promise<number> {
  const url = new URL('/v1/traces', endpoint).toString()
  let sent = 0
  for (let i = 0; i < spans.length; i += BATCH) {
    const body: OtlpTraceRequest = toTraceRequest(spans.slice(i, i + BATCH), version)
    const res = await fetch(url, {
      method: 'POST',
      headers: {'content-type': 'application/json'},
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      throw new Error(`OTLP export to ${url} failed: HTTP ${res.status} ${await res.text()}. Is the collector running?`)
    }
    sent += body.resourceSpans[0]?.scopeSpans[0]?.spans.length ?? 0
  }
  return sent
}
