import {parseDroidSession} from '@domain/harness/droid/parse'
import {buildForest, type Forest} from '@domain/hierarchy'
import type {AgentRun} from '@domain/model'
import {forestSpans, type OtlpSpan} from '@domain/otlp'
import {type DroidPaths, readDroidSessions} from '@infrastructure/droid/reader'

export interface ScanResult {
  runs: AgentRun[]
  forest: Forest
  spans: OtlpSpan[]
  skipped: number
}

export function scanDroid(paths: DroidPaths): ScanResult {
  const inputs = readDroidSessions(paths)
  const runs = inputs.map(parseDroidSession).filter((r): r is AgentRun => r !== undefined)
  const forest = buildForest(runs)
  return {runs, forest, spans: forestSpans(forest), skipped: inputs.length - runs.length}
}
