import {createHash} from 'node:crypto'

import type {Harness} from './model'

/**
 * Trace and span ids are derived from harness ids instead of being random. Any process that knows a
 * run's session id (or a tool call's id) computes the same span id, so a child can point at its parent
 * without ever seeing the parent's record: parts of one workstream exported by different processes,
 * machines or scans, in any order, land in one trace, and re-exporting a run reproduces the same spans.
 */
const hex = (input: string, chars: number): string => createHash('sha256').update(input).digest('hex').slice(0, chars)

export const traceIdFor = (rootKey: string): string => hex(`ember/trace/${rootKey}`, 32)

export const sessionSpanId = (harness: Harness, sessionId: string): string =>
  hex(`ember/span/${harness}/session/${sessionId}`, 16)

export const toolSpanId = (harness: Harness, toolCallId: string): string =>
  hex(`ember/span/${harness}/tool/${toolCallId}`, 16)

export const workstreamRootKey = (workstreamId: string): string => `workstream/${workstreamId}`
export const sessionRootKey = (harness: Harness, sessionId: string): string => `session/${harness}/${sessionId}`
