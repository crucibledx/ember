import {writeFileSync} from 'node:fs'
import {parseArgs} from 'node:util'

import {scanDroid} from '@application/scan'
import {AGENT_KINDS} from '@domain/model'
import {toTraceRequest, traceIdOf} from '@domain/otlp'
import {renderTree} from '@domain/tree'
import {defaultDroidPaths} from '@infrastructure/droid/reader'
import {exportSpans} from '@infrastructure/otlp/exporter'
import {startRelay} from '@infrastructure/otlp/relay'
import type {SessionPlacement} from '@domain/reparent'
import {sessionSpanId} from '@domain/ids'

const VERSION = '0.0.0-poc'
const USAGE = `ember-poc: rebuild Droid workstream -> agent -> subagent -> tool call trees from local session files

  bun run src/main.ts [--factory-home <dir>] [--tree] [--json] [--out <file>] [--endpoint <url>] [--workstream <id>]
                     [--sessions-only] [--relay <port>]

  --tree             print the agent tree (default when nothing else is asked)
  --json             print a summary as one JSON document
  --out <file>       write the OTLP/JSON trace request to a file
  --endpoint <url>   POST spans to an OTLP/HTTP collector, e.g. http://127.0.0.1:14318
  --workstream <id>  only export spans of this workstream
  --sessions-only    export only agent spans; tool spans come from the harness's own telemetry (use with --relay)
  --relay <port>     run a loopback OTLP/HTTP JSON relay that moves Droid's native spans under the Ember tree
                     and forwards everything to --endpoint. Point Droid at it with OTEL_TELEMETRY_ENDPOINT.`

async function main(): Promise<void> {
  const {values} = parseArgs({
    options: {
      'factory-home': {type: 'string'},
      tree: {type: 'boolean', default: false},
      json: {type: 'boolean', default: false},
      out: {type: 'string'},
      endpoint: {type: 'string'},
      workstream: {type: 'string'},
      'sessions-only': {type: 'boolean', default: false},
      relay: {type: 'string'},
      help: {type: 'boolean', default: false},
    },
  })
  if (values.help) return void console.log(USAGE)
  if (values.relay) return runRelay(Number(values.relay), values.endpoint, values['factory-home'])

  const result = scanDroid(defaultDroidPaths(values['factory-home']))
  const {forest} = result
  const nodes = [...forest.nodes.values()]
  const wanted = values.workstream
  const spans = result.spans
    .filter((s) => !wanted || nodes.some((n) => n.workstreamId === wanted && traceIdOf(n) === s.traceId))
    .filter((s) => !values['sessions-only'] || s.name.startsWith('invoke_agent'))

  const count = (kind: string): number => result.runs.filter((r) => r.kind === kind).length
  const linkedSubagents = nodes.filter((n) => n.run.kind === AGENT_KINDS.SUBAGENT && n.parent?.toolCallId).length
  const linkedAgents = nodes.filter((n) => n.run.kind === AGENT_KINDS.AGENT && n.parent?.toolCallId).length
  const summary = {
    sessions: result.runs.length,
    skipped_files: result.skipped,
    orchestrators: count(AGENT_KINDS.ORCHESTRATOR),
    agents: count(AGENT_KINDS.AGENT),
    agents_linked_to_spawn_call: linkedAgents,
    subagents: count(AGENT_KINDS.SUBAGENT),
    subagents_linked_to_task_call: linkedSubagents,
    standalone_sessions: count(AGENT_KINDS.SESSION),
    orphans: forest.orphans.length,
    tool_calls: result.runs.reduce((n, r) => n + r.toolCalls.length, 0),
    workstreams: [...new Set(nodes.map((n) => n.workstreamId).filter(Boolean))].map((id) => ({
      id,
      trace_id: traceIdOf(nodes.find((n) => n.workstreamId === id)!),
    })),
    spans: spans.length,
  }

  if (values.out) writeFileSync(values.out, JSON.stringify(toTraceRequest(spans, VERSION)))
  if (values.endpoint) {
    const sent = await exportSpans(values.endpoint, spans, VERSION)
    console.error(`exported ${sent} spans to ${values.endpoint}`)
  }
  if (values.json) console.log(JSON.stringify(summary, null, 2))
  if (values.tree || (!values.json && !values.out && !values.endpoint)) console.log(renderTree(forest))
}

const RESCAN_MIN_INTERVAL_MS = 1000

/** Sessions start after the relay does, so an unknown session id triggers a rescan (at most once a second). */
function runRelay(port: number, forward: string | undefined, factoryHome: string | undefined): void {
  if (!forward) throw new Error('--relay needs --endpoint <url> of the collector to forward to')
  let placements = new Map<string, SessionPlacement>()
  let scannedAt = 0
  const rescan = (): void => {
    const {forest} = scanDroid(defaultDroidPaths(factoryHome))
    placements = new Map(
      [...forest.nodes.values()].map((n) => [
        n.run.sessionId,
        {traceId: traceIdOf(n), sessionSpanId: sessionSpanId(n.run.harness, n.run.sessionId)},
      ]),
    )
    scannedAt = Date.now()
  }
  rescan()
  startRelay({
    port,
    forward,
    log: (line) => console.error(line),
    resolve: (sessionId) => {
      if (!placements.has(sessionId) && Date.now() - scannedAt > RESCAN_MIN_INTERVAL_MS) rescan()
      return placements.get(sessionId)
    },
  })
  console.error(`ember relay on http://127.0.0.1:${port} -> ${forward} (${placements.size} sessions known)`)
}

main().catch((error: Error) => {
  console.error(`ember-poc: ${error.message}`)
  process.exit(1)
})
