---
id: "0001"
title: "Ember v1"
status: approved
scope: project
target_repos: [ember, web]
assets: none
size: mission
idea_issue: "crucibledx/ember#5"
created: 2026-11-25
---

# 0001: Ember v1

## Problem

A piece of work done by coding agents is spread over many processes: a Droid workstream has an orchestrator, agents
and their Task subagents; a Claude Code review of the same ticket runs elsewhere. Each harness exports its own
telemetry, if any, as disconnected traces. Nobody can answer "which agent did what, under which orchestrator, for which
ticket, and at what cost" in the observability tool they already run.

Evidence, from Droid 1.8.0 and our own workstream (the session-tree POC on branch `poc/session-hierarchy` of
`crucibledx/ember`, and an earlier local Droid telemetry POC whose findings are summarised here):

- Droid `genai` telemetry makes one flat trace per turn. A Task subagent is a separate trace root, and nothing in either
  trace names the other. Workstream (Factory App) sessions export nothing unless the app is started with `OTEL_*` env.
- Droid's `genai` telemetry has no tokens, credits, workstream id or parent link (the `legacy` format's
  `parent_session.id` is a static env value). Droid's local session files have all of them (`callingSessionId`,
  `mission-v2` tags, `tokenUsage.factoryCredits`, `task-invocations.json`).
- The POC rebuilt our workstream (60 sessions, about 12,800 spans) as one trace from those files, with deterministic
  ids so parts exported separately land in one tree, and moved Droid's native spans under it.

For Claude Code, whose beta traces already nest subagents, the gaps are different: no join across sessions or harnesses
for one ticket, no tree when the beta traces are off, MCP server and tool names hidden unless tool inputs are exported
too, and no human attribution. Ember fills those.

Terms used below. A **harness** is a tool that runs AI sessions and has an Ember adapter; v1's harnesses are coding
agents, but neither the adapter contract nor `Run` requires that (§4). A **session** is one harness run (a subagent run
counts as its own session). A **workstream** is a group of sessions a harness runs as one job (Droid: the `missionId`
of an orchestrator and its agents); harnesses without one have none. A **piece of work** is what `ember.work.id` names,
usually a ticket. `ember.agent.kind` is `orchestrator`, `agent`, `subagent` or `session` (a run with no orchestration
around it).

## Complete, don't compete

Factory Analytics shows Droid-only aggregates. Grafana `agento11y` (Claude Code, Cursor and others; no Droid) and Dash0
Darkplane (no Droid) show cost, adoption, sessions and tools, and Dash0 ties sessions to repository, branch and PR.
Datadog's open-source Trajectory is the closest: a local capture service for 30+ clients that links subagents to
parents and agent work to people and PRs; it supports Droid in preview through hooks, and sends to Datadog. Claude
Code's own beta traces nest subagents under the spawning tool span.

What none of them does: send one tree of standard OTLP to whichever backend the user already runs, joined by
deterministic ids across processes, machines and harnesses; model Droid workstreams (orchestrator, agents, subagents)
with Factory credits; and put one `ember.work.id` on work done by different harnesses. Ember runs next to these tools
(it can export to Datadog's or Dash0's OTLP intake) rather than replacing them.

Ember does not build those views. It keeps every harness's native telemetry, fills its gaps from documented settings
and local files, joins everything into one tree with deterministic ids, adds `ember.*` attributes, and sends standard
OTLP to whatever backend the user runs. Ember is metadata only by default. It is a custom OpenTelemetry Collector
distribution, so batching, retries, memory limits and exporters are upstream components, not Ember code
(`crucibledx/hq` `design/ember-go-exception.md`). A harness vendor sees only its own harness, and a platform builds
for its own backend; a neutral cross-harness tree is not either one's job.

## Goals

1. One binary, `ember`, a custom OpenTelemetry Collector distribution in Go, with an *agent* mode for every machine and
   an optional *gateway* mode that merges many machines.
2. Every session, subagent and tool call of a workstream (or of a session with no workstream) forms one trace tree,
   across processes and machines, with deterministic ids. Traces of different harnesses and workstreams for one piece
   of work are joined by `ember.work.id` on every span and metric, and the Agent tree dashboard shows them under one
   work node (§5 explains why the trace is not per work id).
3. Tokens, credits (Droid) or cost (Claude Code), MCP server and tool, `ember.work.id` and human attribution on the
   tree, plus metrics for dashboards.
4. Content capture off by default; a privacy allowlist decides what leaves the machine.
5. Adding a harness is one Go file plus fixtures; the core never names a harness.
6. Install with one command, `ember setup` detects harnesses and writes their documented settings, `ember doctor` says
   what is wrong and how to fix it.
7. Grafana dashboards as code: Agent tree, Overview, Tools and MCP, Cost.

## Non-goals

- Codex, Cursor and other harnesses (Codex is the first follow-up; it must need only `internal/harness/codex.go`).
- AI usage outside coding agents (Grafana assistant, Databricks Genie, Microsoft 365 Copilot). Future direction (#14):
  one view of all AI usage in a company. v1 adds no such source; §4 keeps the adapter contract and `Run` open to them.
- Session browsers, transcripts, prompt or response capture views (agento11y does this). Cost, adoption and ROI views
  beyond the four dashboards (the platforms do this).
- Resource versions (skill or MCP version → outcome) with the Forge lockfile. It needs Forge's manifest, which is not
  approved yet; a follow-up spec covers it. v1 keeps `ember.*` attribute names free for it (`ember.resource.*`).
- A hook receiver. v1 reads documented env and config plus local files; hooks may come later as an adapter input.
- Proxies of model traffic, Docker images, Homebrew and Scoop packages (brew comes after v0.1.0), autostart at login.
- Persistent export queues on disk. Re-scanning session files with deterministic ids is the recovery path.
- Joining a run whose ancestors' files are on no machine running Ember into the ancestors' trace. Cross-machine joins
  work because the files of each run name its trace root: Droid agents record their workstream id, and Task and Claude
  Code subagents are written next to their parent's session on the same machine. A run whose parent files are gone
  becomes its own trace root (§5), keeps `ember.parent.session.id` for queries, and nothing re-parents it later.

## Design

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://github.com/crucibledx/assets/raw/main/ember/diagrams/dark/ember-v1-architecture.svg">
  <img alt="Ember v1 architecture: harnesses, the Ember agent, an optional gateway and the OTLP backend" src="https://github.com/crucibledx/assets/raw/main/ember/diagrams/light/ember-v1-architecture.svg">
</picture>

### 1. Binary, packages and components

Repository layout (Go, per the `engineering-standards` Go section):

```
cmd/ember/main.go             flags and wiring only
builder-config.yaml           OpenTelemetry Collector Builder (ocb) manifest; ocb generates components only
internal/cli/                 setup, doctor, up, down, version, run
internal/config/              Ember config: decode into typed structs, validate in one function
internal/harness/             Adapter interface, registry, harness-neutral model; one file per harness
internal/harness/<name>.go    an adapter; fixtures in internal/harness/testdata/<name>/
internal/tree/                deterministic ids, forest (hierarchy), work id, attribution (pure)
internal/emit/                tree → OTLP spans, logs, metrics (pure)
internal/receiver/sessions/   collector receiver `ember_sessions`: polls adapters, feeds the registry, emits
internal/extension/registry/  collector extension `ember`: session registry shared by receiver and processor, status
internal/processor/ember/     collector processor `ember`
internal/stack/               local Docker-free backend that `ember up` runs when `stack: true`
dashboards/                   generated Grafana JSON; generator in internal/dashboards/
install/install.sh, install.ps1
```

`ocb` generates the component factories with `--skip-compilation`; `cmd/ember` is our own `main` that wraps
`otelcol.NewCommand` under `ember run`. Collector components (exact versions pinned in `builder-config.yaml`, all from
one upstream release):

| Kind | Components |
|---|---|
| Receivers | `otlp` (core), `ember_sessions` (Ember) |
| Processors | `memory_limiter`, `batch` (core), `deltatocumulative` (contrib; Droid metrics are delta), `ember` (Ember) |
| Exporters | `otlp`, `otlphttp`, `debug` (core) |
| Extensions | `ember` (Ember), `bearertokenauth` (contrib; gateway inbound auth) |

Nothing else. A new component needs a line in this spec or a later one. CI builds stripped binaries
(`-trimpath -ldflags "-s -w"`) for darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64, prints each
size, and fails when any binary exceeds **80 MiB** (a published 10-component custom build is about 64 MB; #6
records the measured sizes in the PR so the gate can be lowered).

### 2. Commands

| Command | Behavior | Exit codes |
|---|---|---|
| `ember version [--json]` | `ember 0.1.0 (<commit>, <date>) otelcol <version> <os>/<arch>` | 0 |
| `ember setup [--harness a,b] [--endpoint URL] [--stack] [--profile] [--factory-app] [--adopt-existing] [--hash-key-file FILE] [--lookback DUR] [--dry-run] [--force] [--undo]` | Detects harnesses, writes Ember config and each harness's `Setup` changes with backups, then prints numbered next steps (what changed, what to restart, the next command, the Grafana URL). `--profile` adds one line sourcing `EMBER_HOME/env.sh` (or `env.ps1`) to the shell profile. `--factory-app` also writes the Factory App daemon env (§11). `--adopt-existing` keeps a harness's existing OTLP endpoint as Ember's export target (below). `--dry-run` prints the plan, including download sizes and the number of sessions the first run backfills. `--lookback DUR` sets `sessions.lookback`. `--undo` restores the last setup's backups | 0 ok; 1 error; 2 conflict |
| `ember doctor [--json]` | Runs every check below, one line each: `ok`, `warn` or `fail`, with a fix | 0 no fail; 1 any fail |
| `ember up [--rescan DUR]` | Starts `ember run` detached (pid file, log file), waits up to 10 s for the status endpoint; when the config has `stack: true` (written by `setup --stack`) also starts the local backend, printing the download size before the first download. Already running: prints so, exit 0. Nothing restarts after a reboot; the README has launchd and systemd recipes using `ember run` | 0; 1 with the log tail |
| `ember down` | Stops what `up` started (SIGTERM, then kill after 10 s; on Windows, process kill). Nothing running: exit 0 | 0; 1 |
| `ember run [--mode agent\|gateway] [--collector-config FILE] [--rescan DUR]` | Foreground collector, for `up`, containers and service managers. The config is rendered from the Ember config; `--collector-config` replaces it entirely; `--rescan DUR` as for `up` (§5) | collector's |

`setup` without `--endpoint` or `--stack` asks nothing and defaults to `--stack` on macOS and Linux and fails with a
message naming `--endpoint` on Windows (Open question 5). All commands are non-interactive.

**Existing exports.** If a harness already sends OTLP somewhere else, or the `listen.http` port is taken, `setup`
writes nothing for it and exits 2 with: the file, the key, the current value, Ember's value, and the two ways
forward. `--adopt-existing` makes the existing endpoint Ember's `export.endpoint` (its headers move into
`EMBER_HOME/export.env`, mode 0600, referenced as `${env:…}`), so the user's backend keeps receiving everything plus
Ember's enrichment. `--force` overwrites. Harness endpoints are always derived from `listen.http`.

**First run.** The receiver backfills sessions newer than `sessions.lookback` (default 168 h) once; `setup` says so,
because on a paid backend that is ingest volume.

Doctor checks: config valid; agent running and status endpoint answering; `listen` ports free or held by this user's
Ember (`/status` `user`); for each registered adapter, detected or not, setup applied (values match), session files
readable, parser skipping under 5% of records (`warn` above, naming the harness version), native telemetry seen for
sessions started after setup was applied (backup manifest time; `warn`, not `fail`, because the file-built tree still
works; the fix names the missing step: `source EMBER_HOME/env.sh` or `setup --profile`, `setup --factory-app` and a
daemon restart, restart the harness, or an org-managed override); export endpoint reachable (TCP connect plus TLS
handshake if `https`); content capture off in every harness's config and Ember config (warn if on); attribution files
readable if configured; with `stack: true`, stack binaries present and the stack running (`fail` otherwise).

### 3. Files, configuration and the rendered collector

`EMBER_HOME` (env var; default `~/.ember` from `os.UserHomeDir`) holds `config.yaml`, `export.env` (0600, secrets
referenced from the config), `env.sh`/`env.fish`/`env.ps1` (harness env for shells), `state/sessions.json`,
`logs/ember.log`, `run/ember.pid`, `backups/<UTC timestamp>/manifest.json`, and `stack/`.

```yaml
mode: agent                         # agent | gateway; `ember run --mode` overrides
listen: {grpc: 127.0.0.1:4317, http: 127.0.0.1:4318, status: 127.0.0.1:4319}
export:                             # required; setup writes it from --endpoint or --stack
  endpoint: https://otlp.example.com  # base URL for all signals (OTLP/HTTP appends /v1/<signal>)
  protocol: http/protobuf           # http/protobuf | grpc
  headers: {Authorization: "${env:EMBER_EXPORT_AUTH}"}  # values may only reference env; inline secrets are rejected
  tls: {insecure: false, ca_file: ""}
  compression: gzip                 # gzip | none
  traces: {endpoint: "", protocol: ""}   # optional per-signal overrides; an http endpoint is the full URL
  metrics: {endpoint: "", protocol: ""}  # e.g. http://127.0.0.1:19090/api/v1/otlp/v1/metrics
  logs: {endpoint: "", protocol: ""}     # e.g. http://127.0.0.1:13100/otlp/v1/logs; each may add `headers`
stack: false                        # true: `up` also runs the local stack (§14) and export points at it
sessions: {poll: 5s, lookback: 168h, idle: 30m, native_grace: 2m}
work_id: {branch_patterns: [{match: '(?:^|/)([A-Z][A-Z0-9]+-\d+)', id: '$1'}]}  # `{repo}` in id = <owner>/<repo>
privacy: {content: false, allow: [], deny: [], human_identity: hash, restore_tool_names: true}
attribution: {files: [], default_human: ""}   # files: [{path, format: ember | ledger}]
gateway: {token: "${env:EMBER_GATEWAY_TOKEN}", tls: {cert_file: "", key_file: ""}, insecure: false}
```

Validation (one function over typed structs) names the key and the fix (`export.endpoint: not a URL, e.g.
https://otlp.example.com`). Each signal's effective destination is its override, else the shared value, for endpoint,
protocol and headers. An absent per-signal `headers` key inherits the shared map; a present one, even `{}`, replaces it
(so `{}` sends that signal without the shared credentials); values only `${env:…}`. `ember run`
renders one exporter per signal, `otlphttp/<signal>` (with `<signal>_endpoint` set to the override, else `endpoint` set
to the base URL) or `otlp/<signal>` for `grpc` (`endpoint` only), each with that signal's effective headers and the
shared TLS and compression, and wires each signal's pipeline to its own exporter. Collector config (golden files):

| Mode | Receivers | Processors (traces, logs) | Processors (metrics) |
|---|---|---|---|
| agent | `otlp`, `ember_sessions` | `memory_limiter`, `ember`, `batch` | `memory_limiter`, `ember`, `deltatocumulative`, `batch` |
| gateway | `otlp` with `bearertokenauth` | same | same |

The collector's own metrics are off; its logs go to `logs/ember.log`. `--collector-config` replaces the rendered config.
Both Droid and Claude Code send delta metrics, hence `deltatocumulative`.

### 4. Harness adapter contract

An adapter is one file `internal/harness/<name>.go` in package `harness`, registered in `init()`; a duplicate name
panics at init. The core, including the dashboard generator, iterates the registry. A test fails if any Go file
outside the adapter files and their tests contains, as a string literal, a registered name, a `Mapping.Resource`
value, or a `Mapping.Metrics` name.

```go
type Adapter interface {
    Name() string                                  // "droid"; stable: used in ids and ember.harness
    Detect(env Env) Detection                      // installed?, version, config and session paths
    Setup(env Env, opts SetupOptions) []Change     // documented env and config writes, as data
    Sessions(env Env) ([]SessionRef, error)        // session files and side inputs to watch
    Parse(fsys fs.FS, ref SessionRef) (Run, error) // pure: whole session → harness-neutral Run
    Mapping() Mapping                              // how to read the harness's native OTLP
}
type Env struct { Home, OS string; Getenv func(string) string; LookPath func(string) (string, error)
    Command func(ctx context.Context, name string, args ...string) (string, error) } // 5 s timeout
type Change struct {
    Target     Target   // EnvShell (rendered per shell into EMBER_HOME/env.*) | EnvFile (KEY=VALUE file) | JSONKey
    OS         []string // empty = all
    Path, Key, Value string // JSONKey: Key is a dot path into the file, e.g. env.OTEL_EXPORTER_OTLP_ENDPOINT
    MergeList  bool     // join with an existing comma list; in shells at runtime: ${K:+$K,}value
    Exclusive  bool     // an existing different value is a setup conflict (the export endpoint)
    HeadersKey string   // with Exclusive: where the existing headers live, for --adopt-existing
    ProtocolKey string  // with Exclusive: where the existing protocol lives, for --adopt-existing
    Signal     string   // with Exclusive: "" (all signals) or traces | metrics | logs, the export key adopted into
}
type Mapping struct {
    Resource      map[string][]string // resource attribute → accepted values, e.g. service.name
    SessionKey    string              // attribute holding the session id
    SubagentKey   string              // optional: attribute naming a subagent inside a session
    ToolCallKeys  []string            // first non-empty wins
    ToolNameKeys  []string
    ToolSpanName  string              // only spans with this name, or prefix ending in a space, are tool spans
    RedactedTools []string            // native tool names that hide the real name
    MCP           *regexp.Regexp      // tool name → (server, tool); first match of the separator wins
    NativeAllow   []string            // native attribute keys that may leave the machine
    ContentKeys   []string            // native keys allowed only when privacy.content is true
    OwnerKeys     []string            // attributes naming the human or account that owns the run
    Metrics       NativeMetrics       // native metric names the dashboards use
}
type NativeMetrics struct{ Cost, CostUnit string } // money cost metric and its unit, e.g. "USD"; empty if none
type SessionRef struct{ RunID string; Files []string } // slash paths relative to Env.Home; Parse gets os.DirFS(Home)
```

`EnvShell` conflicts are checked against `Getenv` and the shell profile files. `Run` is the POC's `AgentRun`
(`model.ts`) plus `Cwd`, `Branch`, `Owner`, `HostID`, and usage per model instead of the POC's single model and total:
`Usage map[string]Usage` (model → input, output, cache read, cache write tokens and credits). Adapters fill it per model
where the input records usage per model; usage the input only gives as a total (Droid's `tokenUsage`) always goes under
the model key `unknown`, from its first emission, so a model switch never moves exported usage between series. A run id
is the session id, or `<session id>/<subagent id>` when the harness keeps subagents inside a session. Later adapters may
add fields but not remove methods. The core applies `Change`s, never the adapter.

Nothing here assumes a coding agent. A `Run` needs only harness, session id, kind, status and start and end times;
tool calls, turns, model, usage, parent, `Cwd` and `Branch` may be empty. An adapter without native OTLP returns a zero
`Mapping`, and one with nothing to configure returns no `Change`s. A source read from a vendor API or audit log rather
than local files adds an optional input interface, checked by type assertion, in its own spec; existing adapters and
the core's handling of `Run` do not change.

### 5. The tree and deterministic ids

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://github.com/crucibledx/assets/raw/main/ember/diagrams/dark/ember-v1-session-tree.svg">
  <img alt="Ember v1 session tree: one trace per Droid workstream, native spans re-parented, Claude Code joined by ember.work.id" src="https://github.com/crucibledx/assets/raw/main/ember/diagrams/light/ember-v1-session-tree.svg">
</picture>

Ported from `poc/session-hierarchy` (`ids.ts`, `hierarchy.ts`, `otlp.ts`, `logs.ts`); the same inputs give the same ids:

- `traceId = sha256("ember/trace/" + rootKey)[:32 hex]`; `rootKey` is `workstream/<id>` when the session or an
  ancestor has a workstream id, else `session/<harness>/<top-most known ancestor id>`. The trace is deliberately not
  keyed by `ember.work.id`: one Droid workstream holds agents working on different tickets, and keying by work id
  would split it. Work across harnesses joins on `ember.work.id` (§7 `ember.lineage`, §13 Agent tree).
- `sessionSpanId = sha256("ember/span/<harness>/session/<run id>")[:16]`;
  `toolSpanId = sha256("ember/span/<harness>/tool/<call id>")[:16]`;
  `segmentSpanId(k) = sha256("ember/span/<harness>/session/<run id>/segment/<k>")[:16]` for k ≥ 2.
- Parent of a session span: the spawning tool span when known (Task invocation, or a parent tool result naming the
  child), else the parent's session span. Ancestors are read from files whatever their age (`lookback` limits what is
  emitted, not what is read). A run whose parent file is missing but which has a workstream id itself (a Droid agent on
  a machine without the orchestrator's files) is in the `workstream/<id>` trace with the parent's session span id
  (computable from the parent session id) as parent, as in the POC. A run whose parent file is missing and which has no
  workstream id is held back for up to `sessions.idle`, rechecked each poll; after that it is emitted as the root of its
  own trace (`session/<harness>/<run id>`, no parent span id, `ember.parent.session.id` kept), and that root key is
  stored in state so the run never changes trace. A parent span id is only ever set within the same trace.
- **Segments.** A run's records split wherever two consecutive timestamps are more than `sessions.idle` apart; time `t`
  belongs to segment k if `t` ≤ (last record of k) + `idle`, else to k + 1. Segment 1's span is the session span;
  segment k ≥ 2 is its child and may end after it. A segment span is emitted once, when the segment closes (idle
  elapsed, or the harness records an end), with exactly the segment-span keys of §8.1: identity attributes plus that
  segment's `ember.session.segment`, `ember.agent.llm_turns` and `ember.agent.tool_calls`. Totals, final status and
  children live on metrics (§7). Tool spans and logs are emitted as each call completes, under their segment.
- **State.** `state/sessions.json` (schema version 1) keeps, per run: size and mtime of each file, the last emitted
  segment, the emitted file tool call ids, the time of the last emitted log record, and whether final metrics were sent.
  An item counts as emitted when the next consumer accepted it; the export queue is in memory, so a crash can lose
  queued data. `ember up --rescan <dur>` (and `run --rescan`) re-emits runs active within `dur`, ignoring state; the
  backend may then hold duplicates.
- **Native spans first.** The registry records which runs have native spans and which tool calls were emitted from
  files. After a run's first native span, no more file tool spans are emitted for it once `native_grace` has passed.
  A native tool span takes `toolSpanId` unless a file span with that id was already emitted; then it keeps its own id
  and gets a link to the file span (`ember.link.type=file_tool_span`).

**Work id** (`ember.work.id`), first match wins: the `ember.work.id` resource attribute on the run's native telemetry
(`OTEL_RESOURCE_ATTRIBUTES` at launch); a `work_id.branch_patterns` match on the run's branch (`Run.Branch`, else found
by walking up from `Cwd` to `.git`, following a `gitdir:` file for worktrees and reading `HEAD`; `{repo}` comes from the
origin URL in the git config); the workstream id; the root run id. A descendant inherits its parent's `ember.work.id`
unless it has an explicit or branch match of its own. Ember ships only the generic issue-key pattern; the README shows a
`loop/<n>-` → `{repo}#<n>` example.

### 6. The `ember` processor

1. **Harness match.** A resource belongs to the adapter whose `Mapping.Resource` it matches.
2. **Re-parenting** (port of `reparent.ts`). Spans are grouped by original trace id; each span's run is, first match
   wins: its own `SessionKey` with `SubagentKey`; then, only for a span with no `SessionKey` or whose `SessionKey` is
   the session of the candidate run, the run that owns its tool call id (`ToolCallKeys`, from the parsed files) or the
   run of its parent span (in the batch, else through the bounded map below, which also records span id → run); its own
   `SessionKey` alone; the group root's run. A span with a different `SessionKey` (a separately launched `claude -p`) is
   its own run; its parent relationship comes from the inbound-parent rule below. Claude Code beta traces mix a parent
   and its subagents in one trace, and a subagent tool's `.execution` child carries only `session.id` and `tool_use_id`.
   A session id the registry does not know triggers adapter discovery for that id (`Sessions` filtered by id, at most
   once per second per id) before the run counts as unknown. If the span's start is newer than the run's last parsed
   record, the registry re-parses that run first (at most once per second per run). A run's top spans are its spans
   whose parent is missing or belongs to another run (a group root, or a distinct-session child inside the same trace).
   Known run: every span moves to the run's trace; top spans without an inbound parent (below) get the span of the
   segment containing their start time as parent; tool spans (`ToolSpanName`) get `toolSpanId` (§5), and references to
   old ids are remapped in the batch and, through a bounded map (100,000 entries, 1 h TTL), in later batches;
   `ember.original_trace_id` is added. A top span whose parent span id is set (an inbound `TRACEPARENT`, or the caller's
   span in the same trace) has an inbound parent and is checked against the batch and the bounded map: if Ember moved
   that parent span, the run follows it (new trace id, remapped parent id), and the registry records the parent span's
   run and tool call as this run's parent, so that run's later file spans land in the same trace (items already emitted
   are not re-emitted). A parent Ember did not move is external: the run's spans keep their trace and parent and are
   only enriched. Unknown run: pass through unchanged and count `ember.processor.unresolved`. Nothing is ever dropped.
3. **Logs.** Native records with a run and no trace id get the run's trace id and the tool span id (tool call id
   present) or the segment span id for their timestamp.
4. **Enrichment.** Spans, logs and metric data points with a run get `ember.harness`, `ember.agent.kind`,
   `ember.workstream.id`, `ember.work.id`, `ember.human.started_by`. The registry records the adapter's `OwnerKeys`
   values from native data as `Run.Owner` when the files have none. A native tool span whose tool call id matches a
   file tool call gets that call's `ember.spawned.session_ids`, so the gateway (§10) sees spawns after `native_grace`.
5. **Tool names and MCP.** With `privacy.restore_tool_names`, a native tool name in `RedactedTools` is replaced by the
   name parsed from the session file for the same tool call id (names only, never parameters). A name matching `MCP`
   gets `ember.mcp.server` and `ember.mcp.tool`; `gen_ai.tool.name` keeps the full name.
6. **Privacy** (§8), last.

The status endpoint (`GET /status` on `listen.status`, served by the `ember` extension) returns
`{"version","mode","pid","user","uptime_s","hash_key_id","hash_key_mismatch","harnesses":[{"name","runs","native_last_seen"}],"unresolved","dropped":[{"key","count"}]}`.

### 7. What Ember emits

From session files (`ember_sessions`, resource keys of §8.1): `invoke_agent <agent>` segment spans, `execute_tool
<tool>` spans, the POC's four log events (`ember.agent.started`, `ember.llm.turn`, `ember.tool.call`,
`ember.agent.ended`), each on its span, with bodies built from names, enums and numbers and one resource per run, all
with exactly the keys of §8.1 (the POC's `gen_ai.agent.id`, reserved by semconv for hosted agents, its native-turn
links and its per-span token totals are dropped); and:

| Metric (OTel name; Prometheus name) | Type, unit | Attributes |
|---|---|---|
| `ember.lineage`; `ember_lineage` | gauge, 1 per run, no unit | `session.id`, `ember.parent.session.id`, `ember.root.session.id`, `ember.agent.kind`, `gen_ai.agent.name`, `ember.harness`, `ember.workstream.id`, `ember.work.id`, `ember.human.started_by`, `ember.human.active` (comma-joined, sorted), `ember.agent.status`, `ember.agent.children` |
| `ember.usage.tokens`; `ember_usage_tokens_total` | cumulative sum, `{token}` | `session.id`, `ember.harness`, `gen_ai.request.model`, `gen_ai.token.type` (`input`, `output`, `cache_read`, `cache_write`), `ember.workstream.id`, `ember.work.id` |
| `ember.usage.credits`; `ember_usage_credits_total` | cumulative sum, `{credit}` | as above minus token type (harnesses that report credits) |
| `ember.tool.calls`; `ember_tool_calls_total` | cumulative sum, `{call}` | `session.id`, `ember.harness`, `gen_ai.tool.name`, `ember.mcp.server`, `ember.mcp.tool`, `error.type`, `ember.workstream.id`, `ember.work.id` |

`gen_ai.request.model` on the usage sums is the `Run.Usage` key. Sums start at the run's start time and only grow.
Points are sent each poll for runs active within `idle`, once more when a run closes, and once at startup for each run
within `lookback`; dashboards use `last_over_time`. Prometheus names assume the default `UnderscoreEscapingWithSuffixes`
translation (Prometheus, Mimir, Grafana Cloud); the local stack pins it. Cost in money stays native (Claude Code
`claude_code.cost.usage`, USD), enriched in §6.4.

### 8. Privacy

- `privacy.content: false` by default, and `setup` writes each harness's content switches off. With `content: true`,
  `setup` leaves those switches alone (the user turns them on), adapters' `ContentKeys` pass and log bodies are kept.
- Every attribute (resource, scope, span, span event, link, log, data point) leaves the machine only if its key is on
  the allowlist: every key in §8.1, the `NativeAllow` of the adapter matched in §6.1 (none for data no adapter
  matches), and `privacy.allow`; minus `privacy.deny` (deny wins). `user.email` is never on it.
- With content off, native log bodies become their `event.name` value (or empty) and span status messages are
  removed (codes stay). Span, event and scope names pass unchanged; neither harness documents content in them.
- Human ids follow `privacy.human_identity`: `hash` (default, `h1:` + `hex(hmac_sha256(key, id))[:16]`), `plain`, or
  `off`. The key is one per deployment, shared by every agent and gateway sending to one backend: `setup` takes it,
  first found, from `EMBER_HASH_KEY` in the environment, `--hash-key-file FILE`, or the key already in `export.env`, and
  generates a random one only when none exists, writing it to `export.env`. A provided key that differs from the
  persisted one is a conflict (exit 2; `--force` replaces it); the next steps print the file to copy to other machines.
  `doctor` and `/status` show the key id (`sha256(key)[:8]`, never the key), and agents add it to every resource they
  export as `ember.hash_key.id`; a gateway counts batches whose id differs from its own in `/status` `hash_key_mismatch`
  and `doctor` on the gateway fails on it. This covers `ember.human.*` (each id of the comma list) and every adapter's
  `OwnerKeys` (Droid `user.id`, Claude Code `user.account_uuid`). Hashing skips a value that already starts with `h1:`,
  so a gateway applying privacy again leaves agent-hashed ids unchanged.
- Parsers never copy prompts, tool inputs (except Claude Code's `subagent_type`), results, titles or paths into `Run`;
  `Cwd` is only read for the git lookup and is never exported.
- `/status` and `ember doctor` list dropped keys with counts, never values.

### 8.1 Keys Ember emits

| Where | Keys |
|---|---|
| Ember's resource | `service.name` (`ember`), `service.version`, `service.instance.id` (run id), `host.name`, `os.type`, `host.arch`, `ember.hash_key.id` |
| Segment span | `session.id`, `gen_ai.operation.name`, `gen_ai.conversation.id`, `gen_ai.agent.name`, `gen_ai.request.model`, `ember.harness`, `ember.agent.kind`, `ember.workstream.id`, `ember.work.id`, `ember.parent.session.id`, `ember.parent.tool_call.id`, `ember.human.started_by`, `ember.human.active`, `ember.session.segment`, `ember.agent.llm_turns`, `ember.agent.tool_calls` |
| Tool span | `session.id`, `gen_ai.operation.name`, `gen_ai.tool.name`, `gen_ai.tool.call.id`, `error.type`, `ember.harness`, `ember.work.id`, `ember.tool.incomplete`, `ember.mcp.server`, `ember.mcp.tool`, `ember.spawned.session_ids`; on links `ember.link.type` |
| Log record | `event.name`, `session.id`, `gen_ai.agent.name`, `gen_ai.request.model`, `gen_ai.tool.name`, `gen_ai.tool.call.id`, `error.type`, `ember.harness`, `ember.agent.kind`, `ember.agent.status`, `ember.workstream.id`, `ember.work.id`, `ember.tool.duration_ms`, `ember.tool.incomplete`, `ember.llm.turn` |
| Metric point | the attributes of §7's table |
| Added to native data (§6) | `ember.harness`, `ember.agent.kind`, `ember.workstream.id`, `ember.work.id`, `ember.human.started_by`, `ember.original_trace_id`, `ember.mcp.server`, `ember.mcp.tool`, `ember.spawned.session_ids`; on resources `ember.hash_key.id`; on links `ember.link.type` |

### 9. Human attribution

`ember.human.started_by` and `ember.human.active` on segment spans and `ember.lineage`. Sources:

1. Attribution files. Format `ember`: JSONL `{"at","human","source","session_id","role":"started"|"active"}`, id
   `<source>:<human>`. Format `ledger` reads the Crucible Slack bridge ledger lines (`at`, `event`, `user`, `agent` =
   session id) as `source: slack`, `event: agent` = started, other events = active. Bad lines are skipped and counted;
   files are re-read on change.
2. `Run.Owner` as `<harness>:<owner>` (Droid `session_start.owner`, observed to be the local OS user; Claude Code
   `user.account_uuid`).
3. `attribution.default_human`.

Started by: the earliest `started` record for the run, else the nearest ancestor's, else 2, else 3. Active: every id
with a record for the run or its descendants within the run's time range, plus the started-by id.

### 10. Gateway mode

`ember run --mode gateway` receives OTLP from agents and exports to the backend. It has no `ember_sessions` receiver
and no transcripts, so its `ember` processor applies only the MCP split, privacy and the spawn-link fix below. A
non-loopback `listen` needs `gateway.token` and TLS (`gateway.tls`), or `gateway.insecure: true`, which `doctor` warns
about. One instance per backend: an agent session whose orchestrator ran on another machine cannot see the spawn
call, so the gateway maps `ember.spawned.session_ids` on spawn tool spans to that span's id (100,000 entries, 24 h TTL)
and re-parents a later agent session span that has `ember.parent.session.id` but no `ember.parent.tool_call.id`, only
when both spans are in the same trace (Droid agents carry their workstream id, so they are). An agent span that arrives
before the spawn span stays under the orchestrator's session span. The gateway never changes a span's trace id.

### 11. Droid adapter (`internal/harness/droid.go`)

- Detect: `droid` on `PATH` or `~/.factory/` exists; version via `Env.Command("droid", "--version")`.
- Setup (Droid's documented telemetry env): `OTEL_TELEMETRY_ENDPOINT=http://<listen.http>`,
  `OTEL_TELEMETRY_FORMAT=genai`, `OTEL_LOG_MESSAGE_CONTENT=false`, as `EnvShell` changes. User settings cannot hold
  them (Droid ignores a user-level `telemetry` block), so shells get them through `--profile` (`~/.zshrc`; `~/.bashrc`
  and `~/.bash_profile` if present; `~/.config/fish/conf.d/ember.fish`; PowerShell `$PROFILE`). `--factory-app` also
  writes them as `EnvFile` lines to `~/.config/droid/daemon.env`, which the Factory App daemon loads; setup then prints
  that the daemon must be restarted, and that this file is not in Factory's public docs. No `OTEL_RESOURCE_ATTRIBUTES`
  is written: in a shell it would tag every OTel process. Droid 1.8.0 `genai` emits spans with content off (verified).
- Sessions: `~/.factory/sessions/*/<id>.jsonl`, `<id>.settings.json`, `~/.factory/state/task-invocations.json`.
- Parse: the POC's `parse.ts` and `schema.ts`: tool calls from `tool_use`/`tool_result`, `SpawnAgent` results naming
  children, kind from `mission-v2` tags or `subagent`, workstream from `missionId`, parent from `callingSessionId` or
  the Task invocation, tokens and `factoryCredits` from `tokenUsage` (not `inclusiveTokenUsage`, which counts children
  again), `owner`, `cwd` and `hostId` from `session_start`. The POC fixture moves to `testdata/droid/`.
- Mapping: `service.name` `cli`; `session.id`; tool call `gen_ai.tool.call.id`; tool name `gen_ai.tool.name`; tool span
  `execute_tool `; owner `user.id`; MCP `^(.+?)___(.+)$` (Droid's hooks docs name MCP tools `<server>___<tool>`; the
  same form was observed in 197 `genai` executions); `NativeAllow`: `user.id`, `organization.id`, `model.id`,
  `parent_session.id`, `gen_ai.operation.name`, `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.provider.name`,
  `gen_ai.tool.type`, `gen_ai.agent.name`, `tool.category`, `tool.runtime`, `tool.succeeded`, `session.autonomy_mode`,
  `environment.type`, `skill.location`, `hook.event_name`, `mcp.server`, `terminal.type`.

### 12. Claude Code adapter (`internal/harness/claude.go`, name `claude-code`)

From the Claude Code docs (monitoring-usage, hooks, sub-agents). The transcript format is documented as internal, so
the parser validates every record and skips what it does not know.

- Detect: `claude` on `PATH` or `~/.claude/` exists; version via `Env.Command`. Managed settings (`/Library/Application
  Support/ClaudeCode/managed-settings.json`, `/etc/claude-code/managed-settings.json`, `C:\Program
  Files\ClaudeCode\managed-settings.json`) that set any generic or per-signal OTLP endpoint or protocol are a setup
  conflict that setup cannot resolve: Ember never writes managed files, so for those signals it writes nothing, prints
  that Claude Code sends them straight to the managed destination (the file-built tree still works), and exits 2;
  `--force` and `--adopt-existing` do not apply to managed values and `doctor` warns. Otherwise conflicts are checked
  per signal on the effective value (process env via `Getenv`, then user settings): `--adopt-existing` moves each
  signal's existing endpoint, protocol and headers (headers into `export.env`) to `export.<signal>` (generic values to
  `export`), and `--force` overwrites.
- Setup: `JSONKey` changes in the `env` object of `~/.claude/settings.json`, other keys and their order untouched:
  `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`,
  `OTEL_TRACES_EXPORTER=otlp`, `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1` (Open question 2),
  `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`, `OTEL_EXPORTER_OTLP_ENDPOINT=http://<listen.http>` (`Exclusive`, headers
  in `env.OTEL_EXPORTER_OTLP_HEADERS`), the same per signal (`OTEL_EXPORTER_OTLP_<TRACES|METRICS|LOGS>_ENDPOINT` =
  `http://<listen.http>/v1/<signal>` and `_PROTOCOL` = `http/protobuf`, each `Exclusive` with its `Signal`, headers in
  `env.OTEL_EXPORTER_OTLP_<SIGNAL>_HEADERS` and protocol in `env.OTEL_EXPORTER_OTLP_<SIGNAL>_PROTOCOL`), because Claude
  Code prefers per-signal values, and `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS`,
  `OTEL_LOG_TOOL_CONTENT` = `0`. Claude Code does not pass `OTEL_*` to subprocesses. `doctor` warns when
  `.claude/settings*.json` in the current directory overrides any of them.
- Sessions: `~/.claude/projects/*/<session id>.jsonl` and `.../<session id>/subagents/agent-<agent id>.jsonl`.
- Parse: `type`, `timestamp`, `sessionId`, `cwd`, `gitBranch`; `message.model` and `message.usage` token fields on
  assistant records; `tool_use`/`tool_result` blocks by id. A subagent file is run `<session id>/<agent id>`, kind
  `subagent`; its parent tool call is the call whose result has `toolUseResult.agentId` equal to the agent id, and its
  `subagent_type` comes from that call's input. Claude Code has no orchestrator or workstream.
- Mapping: `service.name` `claude-code`, `claude-code-desktop`; `session.id`; subagent `agent_id`; tool call
  `tool_use_id`, `gen_ai.tool.call.id`; tool name `tool_name`; owner `user.account_uuid`; tool span exactly
  `claude_code.tool` (its `.execution` and `.blocked_on_user` children share the tool call id and keep their own ids);
  redacted `mcp_tool`, `mcp_other`; MCP `^mcp__(.+?)__(.+)$`; cost metric `claude_code.cost.usage` (USD); `NativeAllow`:
  `prompt.id`, `event.sequence`, `agent_id`, `parent_agent_id`, `tool_use_id`, `tool_name`, `success`, `duration_ms`,
  `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `model`, `type`,
  `decision`, `source`, `user.account_uuid`, `organization.id`, `terminal.type`; `ContentKeys`: `prompt`,
  `tool_parameters`, `tool_input`, response bodies.
- Expected tree: native subagent spans nest under the parent's `claude_code.tool` span; Ember's subagent session span
  is a sibling of them under the same (renamed) tool span. Without the beta, Ember's file spans are the tree.

### 13. Dashboards

Generated Grafana JSON in `dashboards/` from `internal/dashboards/` (a build-time generator; nothing added to the
`ember` binary). CI fails if the committed JSON differs from the generator output. Data sources are variables
(Prometheus-compatible, Tempo, Loki), so the files work on any Grafana.

- **Agent tree:** node graph from `ember_lineage` (work → workstream → orchestrator → agents → subagents), filterable
  by work id, harness and human; click-through to Tempo by TraceQL on `session.id`.
- **Overview:** runs by kind, tool calls, errors, active humans, per harness and work id.
- **Tools and MCP:** calls and error rate by tool and by MCP server and tool; durations from spans; log → trace links.
- **Cost:** tokens by type and model; credits and each adapter's money metric side by side, never converted.

Queries use only §7's Prometheus names and adapters' `Mapping.Metrics`.

### 14. Local stack, install and release

- With `stack: true`, `ember up` (macOS, Linux; M3) runs Prometheus, Tempo, Loki and Grafana from pinned official
  archives, downloaded on first use into `EMBER_HOME/stack/` after printing the size (about 2 GB) and verified by
  SHA-256, all on `127.0.0.1` with fixed ports: Grafana 13000, Prometheus 19090, Loki 13100, Tempo 13200 (OTLP gRPC
  14317). These are the POC stack's ports. `setup --stack` writes the per-signal export endpoints for them (traces over
  gRPC to 14317) from M1 (#10a); `up` starts the stack only from M3, so in M1 the same config exports to the running POC
  stack. Windows prints that `--stack` is unsupported.
- `install/install.sh` (macOS, Linux): detects OS and arch, downloads `ember_<version>_<os>_<arch>.tar.gz` and
  `checksums.txt` from GitHub Releases (`EMBER_VERSION` pins, default latest), verifies SHA-256, installs to
  `~/.local/bin/ember` (`EMBER_INSTALL_DIR` overrides) without sudo, prints the `PATH` line when needed, runs `ember
  setup --dry-run` and prints `ember setup` as the next command; it never changes harness settings itself.
  `install/install.ps1` (`irm https://crucibledx.dev/ember/install.ps1 | iex`) does the same into
  `%LOCALAPPDATA%\Programs\ember` (`.zip`) and updates the user `PATH`. The binary is unsigned in v0.1.0. The web repo
  serves both scripts at `crucibledx.dev/ember/`.
- GoReleaser builds the five targets on a tag Nazar pushes; agents never tag. The README covers install, setup,
  privacy, every config key, launchd and systemd recipes with `ember run`, uninstall (`down`, `setup --undo`, remove
  the profile line, delete `EMBER_HOME` and the binary), "add a harness", and a Grafana Cloud recipe.

## Rollout

| Milestone | Work (PRs) | Rows |
|---|---|---|
| M1 (week 3): Droid end to end on our four workstreams | #6 bootstrap; #7 adapter contract; #8 Droid; #9 in three PRs: 9a tree and emit, 9b receiver, registry and state, 9c processor; #10 in two PRs: 10a setup and undo, 10b config, run, up, down, doctor, status; #11 dashboards; #12 dogfood against the POC stack | AC-1 to AC-31 |
| M2 (weeks 4–5) | new issues: Claude Code adapter; human attribution; gateway | AC-32 to AC-38 |
| M3 (week 6): v0.1.0 | new issues: local stack; install scripts and release; README; web serves the scripts | AC-39 to AC-43 |

Every PR stays under about 1,500 changed lines. After merge the orchestrator opens the M2 and M3 issues with their
rows. `CHANGELOG.md` starts at v0.1.0. Until then commits are `docs: ...` or `chore: ...`.

## Open questions

1. **Work id order.** The earlier recommendation was explicit id, then Droid workstream id, then branch issue key.
   That never joins a Droid workstream agent and a Claude Code review of the same ticket, because the workstream id
   always wins for Droid. This spec keeps the workstream in its own attribute (`ember.workstream.id`, which also picks
   the trace) and orders `ember.work.id` as explicit, branch, workstream, root. Confirm.
2. **Claude Code beta traces.** `setup` turns on `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`. Without it Ember still builds
   the tree from transcripts but loses Claude Code's model-call and tool timings. Keep it on by default?
3. **Restoring redacted MCP names.** Claude Code hides MCP names unless `OTEL_LOG_TOOL_DETAILS=1`, which also exports
   tool inputs. `privacy.restore_tool_names: true` restores only the names from the transcript. Default on?
4. **Human ids hashed by default.** `privacy.human_identity: hash` keeps Slack ids and OS user names off the wire but
   makes "who" panels show hashes; our own dogfood would set `plain`. Keep `hash` as the default?
5. **Default destination.** `setup` with no flags picks the local stack (about 2 GB download) on macOS and Linux and
   fails asking for `--endpoint` on Windows. Alternatives: always require `--endpoint` or `--stack`, or default to the
   `debug` exporter. Keep the stack as the default?
6. **Undocumented Droid inputs.** Droid parent links, credits and workstreams come from session files,
   `task-invocations.json` and `daemon.env`, none of them a documented contract. `doctor` warns when parsing degrades.
   Should we ask Factory for a supported format or export of these fields?

## Acceptance contract

Checks run in CI on ubuntu, macos and windows unless a row says otherwise; the Go jobs must be green (Nazar marks them
required). `go test` names are the tests the owning PR adds.

| ID | Behavior that must be true | Check (type) | How to run / evidence |
|---|---|---|---|
| AC-1 | `ember version` prints `ember <semver> (<commit>, <date>) otelcol <version> <os>/<arch>`; `--json` prints `{"version","commit","date","otelcol","os","arch"}` | cli snapshot | #6: `go test ./internal/cli -run TestVersion` |
| AC-2 | The collector components are exactly those in §1 | test | #6: `go test ./cmd/ember -run TestComponentsMatchSpec` |
| AC-3 | CI runs `golangci-lint` (checked-in `version: "2"` config, gofmt and goimports), `go vet` and `go test -race ./...` on three OSes | ci | #6: Go workflow green on the PR |
| AC-4 | CI builds the five targets stripped, prints each size, and fails above 80 MiB | ci | #6: build log shows five sizes; the size check run on an 81 MiB file fails |
| AC-5 | `AGENTS.md` and `.factory/loop-contract.md` name the Go checks (exact commands) instead of Bun | doc | #6: both files list `golangci-lint run`, `go test -race ./...`, `go build` |
| AC-6 | Registering two adapters with one name panics; the registry lists adapters sorted by name | test | #7: `go test ./internal/harness -run TestRegistry` |
| AC-7 | No Go file outside adapter files and their tests contains a registered name, `Mapping.Resource` value or `Mapping.Metrics` name as a literal | test | #7: `go test ./internal/harness -run TestCoreNeverNamesAHarness` |
| AC-8 | Droid parse of the orchestrator/agent/subagent fixture: kinds, workstream, parents, `SpawnAgent` and Task links, tokens and credits from `tokenUsage`, owner, cwd; malformed lines skipped | test | #8: `go test ./internal/harness -run TestDroidParse` |
| AC-9 | Droid `Detect` (fake `Command`), `Setup` with and without `--factory-app`, and every `Mapping` value of §11 | test | #8: `go test ./internal/harness -run TestDroidSetup` |
| AC-10 | A planted secret in Droid fixture prompts, inputs, results, titles and paths never appears in a `Run` | test | #8: `go test ./internal/harness -run TestDroidParseNoContent` |
| AC-11 | Ids match the POC: trace `workstream/w-1` → `4a959b74cdec38fd4344f794a872b43b`; session span `droid`/`s-1` → `af766508c3733a04`; tool span `droid`/`call-1` → `600c8e79f0d6e62e` | test | #9a: `go test ./internal/tree -run TestIDsMatchPOC` |
| AC-12 | A workstream is one trace; agent → spawn call, subagent → Task call, tool → segment; orchestrator and agent exported separately give the same spans except the agent's parent; a subagent whose parent is older than `lookback` is in the workstream trace; a subagent whose parent file is missing is held back, then emitted as its own trace root with no parent span id, and stays in that trace after the parent file appears; an agent emitted on a machine without the orchestrator's files is in the workstream trace under the orchestrator's session span, without waiting; no span has a parent id from another trace; repeated runs are identical | test | #9a: `go test ./internal/tree -run TestForest` (port of `otlp.test.ts`) |
| AC-13 | Segments: split at gaps over `idle`, deterministic; a time inside a gap belongs to the segment §5 names; segment spans carry exactly §8.1's segment keys; segment k ≥ 2 is a child of the session span | test | #9a: `go test ./internal/tree -run TestSegments` |
| AC-14 | `ember.work.id` order of §5, inheritance, worktree `gitdir:` files, a `Cwd` in a subdirectory, `{repo}` from the origin URL | test | #9a: `go test ./internal/tree -run TestWorkID` |
| AC-15 | Logs: one record per agent start and end, model turn and tool call, each on an exported span of the same trace; one resource per run, in time order; failed tool at ERROR | test | #9a: `go test ./internal/emit -run TestLogs` (port of `logs.test.ts`) |
| AC-16 | Metrics of §7 with every listed attribute; sums start at run start and never decrease, also when runs leave `lookback`; a two-model session gives per-model sums; aggregate-only usage is under `unknown` in polls before and after a model switch, and no series ever decreases or double counts; points sent only on the §7 schedule; units as in §7 and the translated Prometheus names equal §7's | test | #9a: `go test ./internal/emit -run TestMetrics` |
| AC-17 | The receiver re-parses a run only when its files changed, at most once per poll; each segment span, tool span and log record is emitted once, also across a restart; `--rescan` re-emits | test | #9b: `go test ./internal/receiver/sessions -run TestEmitOnce` |
| AC-18 | Native first: no file tool spans after `native_grace` once native spans are seen; a native tool span whose file span was already emitted keeps its id and links to it | test | #9b: `go test ./internal/receiver/sessions -run TestNativeFirst` |
| AC-19 | Re-parenting: native roots go under the segment for their start time, including one that arrives before the poll that opens that segment and one for a session the registry has not discovered yet; `ToolSpanName` spans get `toolSpanId`; remap in the same and a later batch; an inbound-parent group keeps its parent when Ember did not move it and follows it (trace and parent remapped, also in a later batch, child run's file spans in the same trace) when Ember did; unknown runs and other services pass through and are counted | test | #9c: `go test ./internal/processor/ember -run TestReparent` (port of `reparent.test.ts`) |
| AC-20 | Harness match by `Mapping.Resource`; enrichment of spans, logs and data points (including a native cost metric); native logs get trace and span ids | test | #9c: `go test ./internal/processor/ember -run TestEnrich` |
| AC-21 | MCP split for `server___tool` and `mcp__server__tool`; `gen_ai.tool.name` unchanged; redacted names restored only with `restore_tool_names` | test | #9c: `go test ./internal/processor/ember -run TestToolNames` |
| AC-22 | Privacy: every key Ember emits is in §8.1 and every §8.1 key survives the filter; native keys pass only for the matched adapter; with defaults a planted secret in every attribute location, a log body and a span status message is not exported; `user.email` dropped; deny beats allow; `content: true` passes only `ContentKeys` and bodies; `human_identity` modes, keyed hash and `user.id`/`user.account_uuid` covered; `/status` lists dropped keys without values | test | #9c: `go test ./internal/processor/ember -run TestPrivacy` |
| AC-23 | `ember setup --dry-run` in a temp `HOME` with Droid present prints the plan and writes nothing; `setup` prints the numbered next steps | cli snapshot | #10a: `go test ./internal/cli -run TestSetupOutput` |
| AC-24 | `setup` writes changes with backups and a manifest of content hashes; a rerun reports nothing to change; an existing endpoint or taken port exits 2 naming file, key, current and wanted value and both options; `--adopt-existing` moves it to `export` with headers in `export.env`; `--stack` writes §14's per-signal endpoints; `--force` overwrites; `--undo` restores files unchanged since setup and refuses, naming the backup, for edited ones; JSON key order is kept; `--harness` and `--profile` (each shell) work; `--hash-key-file` and `EMBER_HASH_KEY` are used instead of generating a key; a second `setup` with neither keeps the persisted key; a different provided key exits 2 | e2e | #10a: `go test ./internal/cli -run TestSetupLifecycle` (real binary, temp `HOME`) |
| AC-25 | A fake adapter added only in a test file appears in `setup --dry-run` and `doctor` with no other code change | test | #10b: `go test ./internal/cli -run TestFakeAdapterNeedsNoCoreChange` |
| AC-26 | Config validation names key and fix; inline header secrets rejected; `${env:…}` expanded; gateway on a non-loopback address without token and TLS refuses to start | test | #10b: `go test ./internal/config -run TestValidate` |
| AC-27 | `ember run` renders §3's pipelines per mode (golden files); `--mode` overrides config; `--collector-config` replaces it; the rendered configs for http, grpc, mixed per-signal overrides and the stack load and validate with the real collector factories | test | #10b: `go test ./internal/cli -run TestRenderConfig` |
| AC-28 | `up` returns once `/status` answers, exits 1 with the log tail after 10 s otherwise, and says when already running; `/status` returns §6's JSON shape, with `unresolved` counting a span of an unknown run; `down` stops it, kills after 10 s, and exits 0 when nothing runs | e2e | #10b: `go test ./internal/cli -run TestUpDown` |
| AC-29 | `doctor`: each §2 check has a case with its `ok`/`warn`/`fail` line and fix, including a port held by another user's Ember, a session after setup without native telemetry (`warn`), parser skips over 5%, a configured stack not running and a gateway with `hash_key_mismatch` above 0; exit 1 on any fail; `--json` prints `{"checks":[{"id","status","message","fix"}]}` | e2e | #10b: `go test ./internal/cli -run TestDoctor` |
| AC-30 | The four dashboards of §13 exist; committed JSON equals generator output; queries use only §7 Prometheus names and adapters' metric names | test | #11: `go test ./internal/dashboards` |
| AC-31 | M1 on the Mac mini: our four workstreams show in the Agent tree (orchestrator → agents → subagents), each workstream is one trace with Droid's native spans under it, tokens and credits per agent are shown; a canary prompt string typed into a dogfood session is absent | screenshot | #12: screenshots of the tree, one trace and Cost; TraceQL span count per workstream; LogQL and TraceQL searches for the canary returning 0 rows |
| AC-32 | Claude Code parse of a main and a subagent fixture: `toolUseResult.agentId` link, `subagent_type`, tokens, branch, MCP names; malformed lines skipped; planted secret absent | test | M2 Claude: `go test ./internal/harness -run TestClaudeParse` |
| AC-33 | Claude Code `Setup` adds §12's keys to `settings.json` `env`, keeps other keys and order; existing generic or per-signal endpoint or protocol in user settings or env is a conflict per signal, `--adopt-existing` moves each signal's endpoint, protocol and headers and the rendered exporter uses them, including a signal with no headers next to shared `Authorization` (`headers: {}`); a managed endpoint writes nothing for that signal, exits 2 and is not overridden by `--force`; `doctor` warns on a project `.claude/settings*.json` override; every `Mapping` value of §12 | test | M2 Claude: `go test ./internal/harness -run TestClaudeSetup` |
| AC-34 | Claude Code native data: interaction roots under the segment span; `claude_code.tool` renamed and its `.execution` child keeps its own id and points at it, across batches; in one beta trace mixing parent and subagent spans each span goes to its own run, including the `.execution` and `.blocked_on_user` children of a subagent tool that carry no `agent_id`, in the same and a later batch; a `claude -p` session launched by a tool, with its own `session.id` under the caller's execution span, both in a separate trace and inside the caller's original trace, is its own run with the caller as parent and lands in the caller's Ember trace; events get trace and span ids | test | M2 Claude: `go test ./internal/processor/ember -run TestClaudeNative` |
| AC-35 | A Droid agent and a Claude Code session on branches with the same issue key get the same `ember.work.id` on all their spans and metrics, and `ember.lineage` gives both runs that work id so the Agent tree query groups them under one work node | test | M2 Claude: `go test ./internal/tree -run TestWorkIDAcrossHarnesses` |
| AC-36 | Attribution from `ember` and `ledger` fixtures: started by, active, inheritance, owner and default fallbacks, bad lines counted, reload on change | test | M2 attribution: `go test ./internal/tree -run TestAttribution` |
| AC-37 | Gateway: requests without the token get 401; two agents (orchestrator, agent) through one gateway give one trace with the agent under its spawn span; an agent span sent before the spawn span stays under the orchestrator session span; with file tool spans suppressed (spawn after `native_grace`) the native spawn span still re-parents the agent; `ember.human.*` and owner ids hashed by an agent arrive at the backend unchanged; the same human emitted by two agents with the shared key gets one id; an agent with a different key is counted in `hash_key_mismatch`; a remote run whose spawn span is in another trace is not re-parented | e2e | M2 gateway: `go test ./internal/cli -run TestGatewayMerge` (three `ember run` processes; the backend is an OTLP server in the test) |
| AC-38 | `ember.human.*` appear on segment spans and `ember.lineage` | test | M2 attribution: `go test ./internal/emit -run TestHumans` |
| AC-39 | `up` with `stack: true` prints the download size first, verifies SHA-256 before extracting (mismatch fails, runs nothing), binds §14's ports on `127.0.0.1`, pins the Prometheus translation, provisions the dashboards | e2e | M3 stack: `go test ./internal/stack -run TestStack` with a fake release server (macos, ubuntu) |
| AC-40 | `install.sh` installs from a fake release server as a non-root user with a read-only `/usr/local`; checksum mismatch exits non-zero and installs nothing; prints the `PATH` line when needed; `shellcheck` passes | e2e | M3 install: `install/test.sh` in CI (macos, ubuntu) |
| AC-41 | `install.ps1` does the same on Windows and updates the user `PATH` | e2e | M3 install: `install/test.ps1` in CI (windows) |
| AC-42 | GoReleaser builds five archives and `checksums.txt`; on PRs it runs as a snapshot without publishing | ci | M3 install: workflow run lists the six files |
| AC-43 | README has the §14 sections; `crucibledx.dev/ember/install.sh` serves the script | doc | M3 README: headings; `curl -fsSL https://crucibledx.dev/ember/install.sh \| head -1` prints `#!/bin/sh` |
