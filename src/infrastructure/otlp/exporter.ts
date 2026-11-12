import type {OtlpLogRecord} from '@domain/logs'
import {toLogsRequest} from '@domain/logs'
import type {OtlpSpan, OtlpTraceRequest} from '@domain/otlp'
import {SERVICE_NAME, toTraceRequest} from '@domain/otlp'

const BATCH = 500

async function post(url: string, body: unknown): Promise<void> {
  const res = await fetch(url, {
    method: 'POST',
    headers: {'content-type': 'application/json'},
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    throw new Error(`OTLP export to ${url} failed: HTTP ${res.status} ${await res.text()}. Is the collector running?`)
  }
}

/** POSTs OTLP/JSON to `<endpoint>/v1/traces`, in batches. */
export async function exportSpans(endpoint: string, spans: OtlpSpan[], version: string): Promise<number> {
  const url = new URL('/v1/traces', endpoint).toString()
  let sent = 0
  for (let i = 0; i < spans.length; i += BATCH) {
    const body: OtlpTraceRequest = toTraceRequest(spans.slice(i, i + BATCH), version)
    await post(url, body)
    sent += body.resourceSpans[0]?.scopeSpans[0]?.spans.length ?? 0
  }
  return sent
}

/** POSTs OTLP/JSON to `<endpoint>/v1/logs`, in batches. */
export async function exportLogs(endpoint: string, records: OtlpLogRecord[], version: string): Promise<number> {
  const url = new URL('/v1/logs', endpoint).toString()
  const ordered = [...records].sort((a, b) => (BigInt(a.timeUnixNano) < BigInt(b.timeUnixNano) ? -1 : 1))
  for (let i = 0; i < ordered.length; i += BATCH) {
    await post(url, toLogsRequest(ordered.slice(i, i + BATCH), {name: SERVICE_NAME, version}))
  }
  return records.length
}
