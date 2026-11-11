// Checks in Tempo that a Droid session, its Task subagents and Droid's own native spans form one trace:
// Ember session span -> native turn -> native `execute_tool Task` -> Ember subagent span -> native subagent turn.
// Prints span names, services and parent links only.
import {sessionRootKey, traceIdFor} from '../src/domain/ids'

const [sessionId, tempo = 'http://127.0.0.1:13200'] = process.argv.slice(2)
if (!sessionId) {
  console.error('usage: bun run scripts/verify-tempo.ts <droid-session-id> [tempo-url]')
  process.exit(2)
}

const toHex = (id: string): string =>
  /^[0-9a-f]+$/i.test(id) ? id.toLowerCase() : Buffer.from(id, 'base64').toString('hex')
interface Row {
  id: string
  parent?: string
  name: string
  service: string
}

const traceId = traceIdFor(sessionRootKey('droid', sessionId))
const res = await fetch(`${tempo}/api/traces/${traceId}`)
if (!res.ok) {
  console.error(`Tempo returned HTTP ${res.status} for trace ${traceId}. Did the export and relay run?`)
  process.exit(1)
}
const body: any = await res.json()
const rows: Row[] = (body.batches ?? body.resourceSpans ?? []).flatMap((rs: any) => {
  const service = rs.resource?.attributes?.find((a: any) => a.key === 'service.name')?.value?.stringValue ?? '?'
  return (rs.scopeSpans ?? rs.instrumentationLibrarySpans ?? []).flatMap((ss: any) =>
    (ss.spans ?? []).map((s: any) => ({
      id: toHex(s.spanId),
      parent: s.parentSpanId ? toHex(s.parentSpanId) : undefined,
      name: s.name,
      service,
    })),
  )
})

const byId = new Map(rows.map((r) => [r.id, r]))
const children = (id?: string): Row[] => rows.filter((r) => (id ? r.parent === id : !r.parent || !byId.has(r.parent)))
const print = (r: Row, depth: number): void => {
  console.log(`${'  '.repeat(depth)}${r.name}  [${r.service}]`)
  for (const c of children(r.id)) print(c, depth + 1)
}
console.log(`trace ${traceId}: ${rows.length} spans`)
for (const root of children()) print(root, 0)

const subagents = rows.filter((r) => r.service === 'ember' && r.parent && byId.get(r.parent)?.service === 'cli')
const checks = {
  'one root': children().length === 1,
  'native Droid spans present': rows.some((r) => r.service === 'cli'),
  'subagent span under native Task span': subagents.some((r) => byId.get(r.parent!)!.name === 'execute_tool Task'),
  'native subagent turn under its Ember span': subagents.some((s) => children(s.id).some((c) => c.service === 'cli')),
}
for (const [name, ok] of Object.entries(checks)) console.log(`${ok ? 'PASS' : 'FAIL'} ${name}`)
process.exit(Object.values(checks).every(Boolean) ? 0 : 1)
