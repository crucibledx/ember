# Ember — Roadmap

> Version-aware telemetry for AI coding environments.

## Core Idea

Other observability tools (Langfuse, Helicone, DataDog LLM Obs) track LLM calls — tokens, cost, latency. What they don't know is **which skill triggered the call** and **which version** of that skill the developer was running.

Ember knows. The Forge compiler emits a manifest on every build. Ember consumes this manifest and correlates it with existing observability data.

> "Forge manages **what** configs agents get. Ember measures **how** those configs are used."

## Tiered Architecture

Each tier adds a new data dimension without replacing the previous one:

| Tier | Data Source | What It Answers | Version |
|------|------------|-----------------|---------|
| **0** (exists) | Forge operational events | "Is Forge working?" | Already in Forge |
| **1** | Forge compiler manifest | "Which skills, versions, agents?" | [v0.1](ember-v0.1.md), [v0.2](ember-v0.2.md) |
| **1.5** | OTLP metric export | "Same data, in Grafana" | [v0.3](ember-v0.3.md) |
| **2** | Langfuse/Helicone import | "Which skill costs how much?" | [v0.4](ember-v0.4.md) |
| **3** | Agent OTEL + file watchers | "Was this skill activated?" | [v0.5](ember-v0.5.md) |

## Version Plan

Each version is a standalone deliverable with room for patch releases:

| Version | Feature | Effort | Depends on |
|---------|---------|--------|------------|
| [v0.1](ember-v0.1.md) | Manifest reader + CLI report | 3-4 days | Forge v0.13 |
| [v0.2](ember-v0.2.md) | Multi-project discovery + aggregation | 1 week | v0.1 |
| [v0.3](ember-v0.3.md) | OTLP export + Grafana dashboard pack | 1-2 weeks | v0.2 |
| [v0.4](ember-v0.4.md) | Langfuse + Helicone integration | 2-3 weeks | v0.3 |
| [v0.5](ember-v0.5.md) | Agent OTEL + file watchers | 4-6 weeks | v0.3 |

**Future versions** (not yet specced):
- **v0.6+** — Cursor/Windsurf/Copilot activation watchers
- **v0.7+** — `@crucibledx/ember-sdk` for LLM call tagging
- **v0.8+** — Watch mode / continuous metric push
- **v1.0** — Standalone `@crucibledx/ember` package extraction

## Where Ember Lives

### Phase 1: Inside Forge CLI

v0.1-v0.3 ship as `forge ember` subcommand. One install, manifest is local.

### Phase 2: Separate package

Extract to `@crucibledx/ember` when Ember needs:
- Long-running processes (OTEL receiver, file watchers)
- Heavy dependencies (Langfuse/Helicone clients)
- Independent release cycle

The manifest JSON is the stable contract between Forge and Ember.

## Competitive Moat

**Nobody else has the manifest.** The manifest requires the compiler. The compiler requires Forge. This is a structural advantage, not a feature delta.

| Tool | What it does | What Ember adds |
|------|-------------|-----------------|
| Langfuse | LLM trace observability | Skill-version attribution |
| Helicone | LLM proxy with logging | Skill-version context enrichment |
| DataDog LLM Obs | Enterprise LLM monitoring | The "which skill version" dimension |
| Claude Code OTEL | Claude-specific telemetry | Cross-agent normalization |
