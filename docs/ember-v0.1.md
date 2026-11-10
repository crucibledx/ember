# Ember v0.1 — Manifest Reader + CLI Report

> **Milestone:** Ember v0.1 — "Read the Manifest"
>
> **Prerequisite:** Forge v0.13 (manifest generation).
>
> **Scope:** Parse `.crucible/forge.manifest.json` from a single project,
> compute basic metrics, display in terminal. The smallest useful thing.
>
> **Effort:** ~3-4 days.

---

## Problem

The Forge compiler emits a manifest on every build. But there's no way to read it, analyze it, or display it in a human-friendly way. The manifest is JSON — useful for machines, not for a developer who wants to know "what did my last compile produce?"

## Solution

A CLI command that reads a single manifest file and renders a terminal report.

## Where It Lives

Phase 1: Inside Forge CLI as `forge ember` subcommand. One install, manifest is local, no transport needed. Extraction to separate package happens when Ember needs long-running processes or heavy dependencies.

## What Ships

1. **Manifest reader** — parse and validate `.crucible/forge.manifest.json`
2. **Metric computation** — derive basic metrics from one manifest
3. **CLI report** — `forge ember` renders metrics in terminal
4. **JSON export** — `forge ember --json` for automation

## Metrics (single project)

| Metric | Derivation |
|--------|-----------|
| Skills deployed | `manifest.skills.length` |
| Skills tree-shaken | `manifest.tree_shaken.length` |
| Per-agent file count | `manifest.agents[agent].files_emitted` |
| Per-agent token total | `manifest.agents[agent].total_tokens` |
| Per-agent budget utilization | `manifest.agents[agent].utilization` |
| Compile duration | `manifest.compile.duration_ms` |
| Last compile time | `manifest.compiled_at` |
| Context detected | `manifest.context.effective` |

## CLI Output

```
$ forge ember

  Forge Manifest Report — my-java-service
  ────────────────────────────────────────
  Compiled: 2026-09-14T16:00:00Z (2 hours ago)
  Duration: 47ms
  Context:  java, spring, gradle

  Skills:   12 deployed, 38 tree-shaken
  Sources:  team-skills (git, main@abc1234)

  Agent         Files  Tokens  Budget   Usage
  ──────────────────────────────────────────────
  claude-code      12   8,400  15,000    56%
  cursor            4   7,200  10,000    72%

  Top skills by token cost:
    study-java         1,200 tokens  (priority: 90)
    review-java        1,100 tokens  (priority: 85)
    detect-project       800 tokens  (priority: 80)
```

## Implementation

```typescript
// Inside forge-cli, new command

// application/commands/ember/index.ts — CommandDefinition
// application/commands/ember/handler.ts — read manifest, compute, render

// domain/ember/reader.ts — parseManifest, validateManifest
// domain/ember/metrics.ts — computeMetrics from single manifest
// infrastructure/cli/renderer.ts — extend with ember report formatting
```

## File Location

```
domain/ember/
  types.ts        — ForgeManifest (imported from compiler or redefined)
  reader.ts       — parseManifest, validateManifest
  metrics.ts      — computeMetrics
  index.ts        — barrel

application/commands/ember/
  index.ts        — CommandDefinition
  handler.ts      — handler logic
```

## Tests

- Parse valid manifest → all fields extracted
- Parse manifest with missing optional fields → defaults
- Invalid JSON → helpful error message
- Missing manifest file → "Run `forge sync` with compile enabled first"
- Metric computation from known manifest → expected values
- `--json` flag → valid JSON to stdout

## Explicitly Out of Scope

- Multi-project aggregation (v0.2)
- OTLP export (v0.3)
- Grafana dashboards (v0.3)
- Langfuse/Helicone integration (v0.4+)
- Agent OTEL / file watchers (v0.5+)
