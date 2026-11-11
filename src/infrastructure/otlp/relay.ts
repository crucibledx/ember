import type {ResolveSession} from '@domain/reparent'
import {reparentHarnessSpans} from '@domain/reparent'

export interface RelayOptions {
  port: number
  /** OTLP/HTTP base URL of the real consumer (collector, Tempo, vendor). */
  forward: string
  resolve: ResolveSession
  log: (line: string) => void
}

/**
 * Minimal OTLP/HTTP JSON relay bound to loopback. Traces are re-parented, metrics and logs are forwarded
 * byte for byte. A production build would sit behind (or inside) a standard OpenTelemetry Collector so
 * protobuf, batching, retry and auth come from the collector rather than from Ember.
 */
export function startRelay(options: RelayOptions): {stop: () => void} {
  const server = Bun.serve({
    hostname: '127.0.0.1',
    port: options.port,
    async fetch(req) {
      const path = new URL(req.url).pathname
      const target = new URL(path, options.forward).toString()
      const contentType = req.headers.get('content-type') ?? ''
      let body: BodyInit = await req.arrayBuffer()
      if (path === '/v1/traces') {
        if (!contentType.includes('json')) {
          options.log(`relay: ${contentType} traces forwarded unchanged (only OTLP/JSON is re-parented)`)
        } else {
          const {request, stats} = reparentHarnessSpans(JSON.parse(new TextDecoder().decode(body)), options.resolve)
          body = JSON.stringify(request)
          options.log(
            `relay: ${stats.reparented}/${stats.spans} spans re-parented, unresolved sessions ${stats.unresolvedSessions.length}`,
          )
        }
      }
      const res = await fetch(target, {method: 'POST', headers: {'content-type': contentType}, body})
      return new Response(await res.arrayBuffer(), {
        status: res.status,
        headers: {'content-type': res.headers.get('content-type') ?? ''},
      })
    },
  })
  return {stop: () => server.stop()}
}
