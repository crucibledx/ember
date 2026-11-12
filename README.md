# Ember POC: harness agent trees as OTLP traces

Throwaway proof of concept for Ember. Not a product.

It answers one question: can we show a whole Droid workstream (orchestrator, agents, Task subagents, tool calls)
as one trace in any OTel backend, mostly by using what Droid already emits?

## What it does

- **Skeleton from session files.** Reads `~/.factory/sessions/**` and `~/.factory/state/task-invocations.json`,
  rebuilds workstream -> agent -> subagent -> tool call, and exports OTLP/JSON spans (`invoke_agent`, `execute_tool`).
  Trace and span ids are sha256 of harness ids, so separate exports join into one trace.
- **Relay for native spans.** Droid's own OTel (`OTEL_TELEMETRY_FORMAT=genai`) makes one flat trace per turn,
  and subagent turns are separate roots. The loopback relay moves those spans under the Ember session span. It
  gives native `execute_tool` spans the deterministic tool span id, so subagents hang under Droid's own Task span.
  Everything else is forwarded unchanged.
- **No content.** Only allowlisted attribute keys are exported (`EXPORTED_ATTRIBUTES` in `src/domain/otlp.ts`).
- **Correlated logs.** `--logs` sends one OTLP log record per agent start and end, model turn and tool call. Each
  record carries the trace and span id of its span in the record's own fields, so a backend can jump from a log line
  to its span and back. Bodies are templates built from names and numbers (`tool Execute ok 812ms`); attributes are
  allowlisted (`EXPORTED_LOG_ATTRIBUTES` in `src/domain/logs.ts`). Records go out in time order, one resource per
  session (`service.instance.id`), because Loki rejects entries much older than the newest one in a stream.
- **Native spans first.** `--tempo <url>` looks up Droid's own turn traces. A session that has them keeps Droid's
  tool spans; Ember drops its own copies and links the session span to each native turn instead.

## Run

Needs Bun. Any OTLP/HTTP backend works; the check below uses `grafana/otel-lgtm` on loopback.

```sh
bun install
bun test src/                       # unit tests
bun run src/main.ts --tree          # print the agent tree from local session files
bun run src/main.ts --json          # counts: sessions, agents, subagents, links, orphans

# end to end: relay + a real droid run + skeleton export + Tempo check
docker run -d --name ember-poc-otel -p 127.0.0.1:13000:3000 -p 127.0.0.1:13200:3200 \
  -p 127.0.0.1:14318:4318 grafana/otel-lgtm:0.37.0
bun run src/main.ts --relay 14320 --endpoint http://127.0.0.1:14318 &
OTEL_TELEMETRY_ENDPOINT=http://127.0.0.1:14320 OTEL_TELEMETRY_FORMAT=genai OTEL_LOG_MESSAGE_CONTENT=false \
  droid exec --auto high "Use the Task tool to launch an explorer subagent that reads a file. Answer in one sentence."
bun run src/main.ts --sessions-only --endpoint http://127.0.0.1:14318
bun run scripts/verify-tempo.ts <droid-session-id>   # prints the span tree and PASS/FAIL checks
```

`--sessions-only` leaves tool spans to Droid's native telemetry, so the same tool call is not exported twice.

Backfill a whole workstream, traces and correlated logs, into a local stack:

```sh
bun run src/main.ts --workstream <orchestrator session id> --endpoint http://127.0.0.1:14318 \
  --logs --tempo http://127.0.0.1:13200 --json
```

## Known limits

- Droid only. Other harnesses would add a parser under `src/domain/harness/<name>/`.
- The relay re-parents OTLP/JSON only (what Droid sends). A product would do this as an OpenTelemetry Collector
  processor and get protobuf, batching, retry and auth from the collector.
- Agent sessions record only `callingSessionId` (no spawn tool-call id). The exact `SpawnAgent` call is recovered from the
  orchestrator's session file, whose result names the agent session; an agent scanned without its orchestrator hangs
  under the orchestrator session span instead.
- A subagent scanned without its parent keeps the right parent span id but lands in a trace of its own.
- Agent status is often `unknown`: session files do not record how an interactive session ended.
- Local session file formats are undocumented and can change with any Droid release.
- Native linking is per session: if Tempo has any Droid turn for a session, all Ember tool spans of that session are
  dropped, even when part of the native telemetry is missing. Tempo's search `limit` bounds how many turns are found.
- Log records are derived from session files, not emitted live; Droid has no native log signal.
