import {z} from 'zod'

/**
 * Droid's local session files are not a documented format, so every record is validated here and anything
 * unexpected is skipped rather than trusted. Only the fields Ember uses are modelled; content blocks keep
 * their `content` as unknown and it is never copied out of the parser.
 */
export const sessionStartSchema = z.object({
  type: z.literal('session_start'),
  id: z.string(),
  callingSessionId: z.string().nullish(),
})

const toolUseBlock = z.object({type: z.literal('tool_use'), id: z.string(), name: z.string(), input: z.unknown()})
const toolResultBlock = z.object({
  type: z.literal('tool_result'),
  tool_use_id: z.string(),
  is_error: z.boolean().optional(),
  content: z.unknown(),
})
const otherBlock = z.object({type: z.string()})
export const contentBlockSchema = z.union([toolUseBlock, toolResultBlock, otherBlock])

export const messageRecordSchema = z.object({
  type: z.literal('message'),
  timestamp: z.string(),
  message: z.object({
    role: z.string(),
    modelId: z.string().optional(),
    content: z.union([z.array(z.unknown()), z.string()]).optional(),
  }),
})

export const anyRecordSchema = z.object({type: z.string(), timestamp: z.string().optional()})

const usageSchema = z.object({
  inputTokens: z.number().default(0),
  outputTokens: z.number().default(0),
  cacheReadTokens: z.number().default(0),
  cacheCreationTokens: z.number().default(0),
})

export const settingsSchema = z.object({
  model: z.string().optional(),
  tokenUsage: usageSchema.optional(),
  tags: z
    .array(
      z.object({
        name: z.string(),
        metadata: z.object({kind: z.string().optional(), missionId: z.string().optional()}).optional(),
      }),
    )
    .default([]),
})
export type DroidSettings = z.infer<typeof settingsSchema>

export const taskInvocationSchema = z.object({
  childSessionId: z.string(),
  parentSessionId: z.string(),
  parentToolUseId: z.string(),
  subagentType: z.string().optional(),
  status: z.string().optional(),
  createdAt: z.number().optional(),
  durationMs: z.number().optional(),
})
export type TaskInvocation = z.infer<typeof taskInvocationSchema>

export const taskInvocationsFileSchema = z.object({invocations: z.array(z.unknown()).default([])})
