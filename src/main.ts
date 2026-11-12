import {writeFileSync} from 'node:fs'
import {parseArgs} from 'node:util'

import {scanDroid} from '@application/scan'
import {AGENT_KINDS} from '@domain/model'
import {forestLogs, toLogsRequest} from '@domain/logs'
import {linkNativeTurns, SERVICE_NAME, toTraceRequest, traceIdOf} from '@domain/otlp'
import {renderTree} from '@domain/tree'
import {defaultDroidPaths} from '@infrastructure/droid/reader'
import {exportLogs, exportSpans} from '@infrastructure/otlp/exporter'
import {findNativeTurns} from '@infrastructure/tempo/native'
import {startRelay} from '@infrastructure/otlp/relay'
import type {SessionPlacement} from '@domain/reparent'
import {sessionSpanId} from '@domain/ids'

const VERSION = '0.0.0-poc'
const USAGE = `ember-poc: rebuild Droid workstream -> agent -> subagent -> tool call trees from local session files

  bun run src/main.ts [--factory-home <dir>] [--tree] [--json] [--out <file>] [--endpoint <url>] [--workstream <id>]
                     [--sessions-only] [--relay <port>] [--logs] [--logs-out <file>] [--tempo <url>]

  --tree             print the agent tree (default when nothing else is asked)
  --json             print a summary as one JSON document
  --out <file>       write the OTLP/JSON trace request to a file
  --endpoint <url>   POST spans to an OTLP/HTTP collector, e.g. http://127.0.0.1:14318
  --workstream <id>  only export spans of this workstream
  --session <id>     only export this session (its trace may also hold its parents and children)
  --logs             also export one OTLP log record per agent start/end, model turn and tool call, carrying
                     the trace and span id of the span it belongs to (to --endpoint /v1/logs)
  --logs-out <file>  write the OTLP/JSON logs request to a file
  --tempo <url>      look up Droid's own turn traces in Tempo; sessions that have them keep the native tool
                     spans (Ember's are dropped) and the Ember session span links to each native turn
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
      session: {type: 'string'},
      'sessions-only': {type: 'boolean', default: false},
      relay: {type: 'string'},
      logs: {type: 'boolean', default: false},
      'logs-out': {type: 'string'},
      tempo: {type: 'string'},
      help: {type: 'boolean', default: false},
    },
  })
  if (values.help) return void console.log(USAGE)
  if (values.relay) return runRelay(Number(values.relay), values.endpoint, values['factory-home'])

  const result = scanDroid(defaultDroidPaths(values['factory-home']))
  const {forest} = result
  const nodes = [...forest.nodes.values()]
  const wanted = values.workstream
  const inScope = nodes.filter(
    (n) => (!wanted || n.workstreamId === wanted) && (!values.session || n.run.sessionId === values.session),
  )
  const traceIds = new Set(inScope.map(traceIdOf))
  const native = values.tempo
    ? await findNativeTurns(
        values.tempo,
        Math.min(...inScope.map((n) => n.run.startedAt)),
        Math.max(...inScope.map((n) => n.run.endedAt)),
      )
    : new Map()
  const nativeSessions = new Set(inScope.map((n) => n.run.sessionId).filter((id) => native.has(id)))
  const spans = linkNativeTurns(
    result.spans.filter((s) => traceIds.has(s.traceId)),
    new Map([...native].filter(([id]) => nativeSessions.has(id))),
  ).filter((s) => !values['sessions-only'] || s.name.startsWith('invoke_agent'))
  const logs = forestLogs(forest, nativeSessions).filter((r) => traceIds.has(r.traceId))

  const count = (kind: string): number => inScope.filter((n) => n.run.kind === kind).length
  const linkedSubagents = inScope.filter((n) => n.run.kind === AGENT_KINDS.SUBAGENT && n.parent?.toolCallId).length
  const linkedAgents = inScope.filter((n) => n.run.kind === AGENT_KINDS.AGENT && n.parent?.toolCallId).length
  const summary = {
    sessions: inScope.length,
    skipped_files: result.skipped,
    orchestrators: count(AGENT_KINDS.ORCHESTRATOR),
    agents: count(AGENT_KINDS.AGENT),
    agents_linked_to_spawn_call: linkedAgents,
    subagents: count(AGENT_KINDS.SUBAGENT),
    subagents_linked_to_task_call: linkedSubagents,
    standalone_sessions: count(AGENT_KINDS.SESSION),
    orphans: forest.orphans.length,
    tool_calls: inScope.reduce((n, node) => n + node.run.toolCalls.length, 0),
    workstreams: [...new Set(inScope.map((n) => n.workstreamId).filter(Boolean))].map((id) => ({
      id,
      trace_id: traceIdOf(nodes.find((n) => n.workstreamId === id)!),
    })),
    spans: spans.length,
    log_records: logs.length,
    error_log_records: logs.filter((r) => r.severityText === 'ERROR').length,
    sessions_with_native_turns: nativeSessions.size,
  }

  if (values.out) writeFileSync(values.out, JSON.stringify(toTraceRequest(spans, VERSION)))
  if (values.endpoint) {
    const sent = await exportSpans(values.endpoint, spans, VERSION)
    console.error(`exported ${sent} spans to ${values.endpoint}`)
    if (values.logs) console.error(`exported ${await exportLogs(values.endpoint, logs, VERSION)} log records`)
  }
  if (values['logs-out'])
    writeFileSync(values['logs-out'], JSON.stringify(toLogsRequest(logs, {name: SERVICE_NAME, version: VERSION})))
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
