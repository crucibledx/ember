import type {Forest, RunNode} from './hierarchy'
import {AGENT_KINDS} from './model'

const minutes = (ms: number): string => `${(ms / 60_000).toFixed(1)}m`
const short = (id: string): string => id.slice(0, 8)

function topTools(node: RunNode, n = 3): string {
  const counts = new Map<string, number>()
  for (const call of node.run.toolCalls) counts.set(call.name, (counts.get(call.name) ?? 0) + 1)
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([name, c]) => `${name}×${c}`)
    .join(' ')
}

/** Text tree of the orchestrated runs. Plain single sessions are summarised as a count. */
export function renderTree(forest: Forest): string {
  const callNames = new Map(
    [...forest.nodes.values()].flatMap((n) => n.run.toolCalls.map((c) => [c.id, c.name] as const)),
  )
  const line = (node: RunNode): string => {
    const {run, parent} = node
    const via = parent?.toolCallId ? ` via ${callNames.get(parent.toolCallId) ?? 'tool'}` : ''
    const label = run.agentType ? `${run.kind}:${run.agentType}` : run.kind
    const span = minutes(run.endedAt - run.startedAt)
    return `${label} ${short(run.sessionId)}${via}  ${span}  ${run.toolCalls.length} tools [${topTools(node)}]  ${run.status}`
  }
  const out: string[] = []
  const walk = (node: RunNode, prefix: string, last: boolean, depth: number): void => {
    out.push(depth === 0 ? line(node) : `${prefix}${last ? '└─ ' : '├─ '}${line(node)}`)
    const next = depth === 0 ? '' : prefix + (last ? '   ' : '│  ')
    node.children.forEach((child, i) => walk(child, next, i === node.children.length - 1, depth + 1))
  }
  const multi = forest.roots.filter((r) => r.children.length > 0 || r.run.kind !== AGENT_KINDS.SESSION)
  for (const root of multi) {
    if (root.workstreamId) out.push(`workstream ${short(root.workstreamId)}`)
    walk(root, '', true, 0)
    out.push('')
  }
  const plain = forest.roots.length - multi.length
  if (plain > 0) out.push(`${plain} standalone session(s) without agents or subagents`)
  if (forest.orphans.length > 0) out.push(`${forest.orphans.length} run(s) whose parent session was not found locally`)
  return out.join('\n')
}
