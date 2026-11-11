import {existsSync, readdirSync, readFileSync, statSync} from 'node:fs'
import {homedir} from 'node:os'
import {join} from 'node:path'

import type {DroidSessionInput} from '@domain/harness/droid/parse'
import {parseTaskInvocations} from '@domain/harness/droid/parse'

export interface DroidPaths {
  /** `~/.factory/sessions`: one folder per working directory, `<id>.jsonl` + `<id>.settings.json` inside. */
  sessionsDir: string
  /** `~/.factory/state/task-invocations.json`: Task tool call -> child session. */
  taskInvocationsFile: string
}

export const defaultDroidPaths = (factoryHome = join(homedir(), '.factory')): DroidPaths => ({
  sessionsDir: join(factoryHome, 'sessions'),
  taskInvocationsFile: join(factoryHome, 'state', 'task-invocations.json'),
})

const readJson = (path: string): unknown => {
  if (!existsSync(path)) return undefined
  try {
    return JSON.parse(readFileSync(path, 'utf8'))
  } catch (error) {
    throw new Error(`Cannot parse ${path}: ${(error as Error).message}. Is Droid writing it right now? Retry the scan.`)
  }
}

export function readDroidSessions(paths: DroidPaths): DroidSessionInput[] {
  if (!existsSync(paths.sessionsDir)) {
    throw new Error(`No Droid sessions folder at ${paths.sessionsDir}. Pass --factory-home if Droid lives elsewhere.`)
  }
  const invocations = parseTaskInvocations(readJson(paths.taskInvocationsFile))
  const inputs: DroidSessionInput[] = []
  for (const dir of readdirSync(paths.sessionsDir)) {
    const folder = join(paths.sessionsDir, dir)
    if (!statSync(folder).isDirectory()) continue
    for (const file of readdirSync(folder)) {
      if (!file.endsWith('.jsonl')) continue
      const sessionId = file.slice(0, -'.jsonl'.length)
      const settingsPath = join(folder, `${sessionId}.settings.json`)
      let settings: unknown
      try {
        settings = readJson(settingsPath)
      } catch {
        // settings are rewritten while a session runs; a half-written file only loses tags/tokens for this scan
        settings = undefined
      }
      inputs.push({
        sessionId,
        lines: readFileSync(join(folder, file), 'utf8').split('\n').filter(Boolean),
        settings,
        invocation: invocations.get(sessionId),
      })
    }
  }
  return inputs
}
