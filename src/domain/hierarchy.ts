import {AGENT_KINDS, type AgentRun} from './model'

export interface RunNode {
  run: AgentRun
  /** Parent as resolved against the runs that were found, with the spawning tool call filled in. */
  parent?: {sessionId: string; toolCallId?: string}
  children: RunNode[]
  /** Workstream id, inherited from the nearest ancestor that has one. */
  workstreamId?: string
  /** Key of the trace this run belongs to: its workstream, else its top-most known ancestor. */
  rootKey: {type: 'workstream'; id: string} | {type: 'session'; id: string}
}

export interface Forest {
  roots: RunNode[]
  nodes: Map<string, RunNode>
  /** Runs whose recorded parent was not among the scanned runs. They still export, under the parent's span id. */
  orphans: RunNode[]
}

/**
 * Finds the parent tool call that started `child` when the harness only records it on the parent side
 * (Droid `SpawnAgent` results carry the new session id).
 */
const spawningCall = (parent: AgentRun, childId: string): string | undefined =>
  parent.toolCalls.find((call) => call.spawnedSessionIds.includes(childId))?.id

export function buildForest(runs: AgentRun[]): Forest {
  const nodes = new Map<string, RunNode>()
  for (const run of runs) {
    nodes.set(run.sessionId, {run, children: [], rootKey: {type: 'session', id: run.sessionId}})
  }

  const orphans: RunNode[] = []
  for (const node of nodes.values()) {
    const ref = node.run.parent
    if (!ref || ref.sessionId === node.run.sessionId) continue
    const parentNode = nodes.get(ref.sessionId)
    node.parent = {sessionId: ref.sessionId, toolCallId: ref.toolCallId}
    if (!parentNode) {
      orphans.push(node)
      continue
    }
    node.parent.toolCallId ??= spawningCall(parentNode.run, node.run.sessionId)
    parentNode.children.push(node)
  }

  const roots = [...nodes.values()].filter((n) => !n.parent || !nodes.has(n.parent.sessionId))
  const assign = (node: RunNode, inherited: RunNode['rootKey'], workstreamId?: string): void => {
    node.workstreamId = node.run.workstreamId ?? workstreamId
    node.rootKey = node.workstreamId ? {type: 'workstream', id: node.workstreamId} : inherited
    for (const child of node.children) assign(child, node.rootKey, node.workstreamId)
  }
  for (const root of roots) {
    const fallback: RunNode['rootKey'] = {type: 'session', id: root.parent?.sessionId ?? root.run.sessionId}
    assign(root, fallback)
  }

  const byStart = (a: RunNode, b: RunNode): number => a.run.startedAt - b.run.startedAt
  roots.sort(byStart)
  for (const node of nodes.values()) node.children.sort(byStart)
  return {roots, nodes, orphans}
}

export const isOrchestrated = (node: RunNode): boolean =>
  node.run.kind !== AGENT_KINDS.SESSION || node.children.length > 0
