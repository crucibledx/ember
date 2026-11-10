# Ember — Agent Context

> Cross-tool AI assistant observability — version-aware telemetry for AI coding environments.

## Identity

- **Status:** Design phase — docs only, no code and no commits yet
- **Position:** Second product in the Crucible platform
- **GitHub:** `crucibledx/ember`
- **CI:** `.github/workflows/ci.yml` is copied from forge-cli (format:check, lint, typecheck, `bun test src/`). There is no `package.json` or `src/` yet, so it will fail until code exists.

## What It Does

Ember measures how AI assistant configurations are used, which skill versions are deployed, and whether updates help or hurt — across every tool a team uses. It answers questions nobody else can by correlating Forge's skill manifest with observability data.

> "Forge manages **what** configs agents get. Ember measures **how** those configs are used."

## The Gap

Other observability tools (Langfuse, Helicone, DataDog LLM Obs) track LLM calls — tokens, cost, latency. They don't know **which skill triggered the call** or **which version** of that skill the developer was running. Ember knows, because the Forge compiler emits a manifest on every build.

## Architecture — Progressive Tiers

| Tier | Data Source | What It Answers |
|------|-------------|-----------------|
| **0** | Forge operational events | "Is Forge working?" |
| **1** | Forge compiler manifest | "Which skills, versions, agents per project?" |
| **1.5** | OTLP metric export | "Same data, in Grafana/DataDog" |
| **2** | Langfuse/Helicone import | "Which skill costs how much?" |
| **3** | Agent OTEL + file watchers | "Was this skill actually activated?" |

## Integration Strategy

Ember integrates with existing tools rather than replacing them:
- **Langfuse** — correlate skill versions with LLM traces
- **Helicone** — enrich proxy logs with skill attribution
- **Grafana** — pre-built dashboard pack
- **DataDog** — custom metrics with skill-version dimensions
- **Claude Code OTEL** — native event ingestion + version enrichment

## Design Docs

| Doc | Scope |
|-----|-------|
| `docs/ember-v0.1.md` | Tier 0 + 1 — manifest consumption, basic dashboard |
| `docs/ember-v0.2.md` | Tier 1.5 — OTLP export |
| `docs/ember-v0.3.md` | Tier 2 — Langfuse/Helicone integration |
| `docs/ember-v0.4.md` | Tier 3 — Agent OTEL + file watchers |
| `docs/ember-v0.5.md` | Advanced analytics |
| `docs/ember-roadmap.md` | Full roadmap overview |

## Data Bridge

Ember's primary data source is `.crucible/forge.manifest.json` — planned to be written by Forge's compiler (spec: forge-cli `docs/forge-v0.7.6.md`, not shipped; Forge is currently 0.6.x). The manifest captures skill IDs, versions, hashes, sources, per-agent targets, token estimates, and tree-shaking decisions.

## Research

Relevant research in the workspace root (`~/crucible`):
- `research/crucible-ember-research.md`
- `research/ember-evolution-research.md`
- `research/ember-validation-research.md`
- `research/crucible-observability-product.md`
- `research/forge-version-awareness.md` — user feedback validating Ember's thesis

## Dependencies

- **Requires** the planned Forge manifest generation (v0.7.6 spec) to have real data
- **No code dependencies** — Ember will be its own package/binary
- Will likely use TypeScript/Bun to match Forge's stack

## What's Needed to Start Building

1. Forge compiler pipeline (v0.7.x) must ship — Ember needs manifest data
2. Pick initial scope: Tier 0+1 = local manifest reading + basic CLI dashboard
3. Decide stack: likely `@crucibledx/ember` CLI, same TypeScript/Bun pattern as Forge
