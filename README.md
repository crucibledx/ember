# Ember

**Cross-tool AI assistant observability — version-aware telemetry for AI coding environments.**

Ember is the second product in the [Crucible](https://crucibledx.dev) platform. It measures how AI assistant configurations are used, which skill versions are deployed, and whether updates help or hurt — across every tool your team uses.

> "Forge manages **what** configs agents get. Ember measures **how** those configs are used."

## Status

🔬 **Design phase** — architecture spec in progress. See [`docs/ember-v0.1.md`](docs/ember-v0.1.md).

## Core Idea

Other observability tools (Langfuse, Helicone, DataDog LLM Obs) track LLM calls — tokens, cost, latency. What they don't know is **which skill triggered the call** and **which version** of that skill the developer was running.

Ember knows. The Forge compiler emits a manifest on every build listing every skill's ID, version, hash, source, and per-agent target. Ember consumes this manifest and correlates it with existing observability data to answer questions nobody else can:

- "Skill X v2.1.0 costs 40% more tokens than v2.0.0 — is the quality improvement worth it?"
- "92% of developers have the latest skill version synced, but only 67% are on the latest for review-java"
- "Team A uses Claude Code for 48% of sessions, Team B prefers Cursor at 60%"

## Architecture

Ember grows progressively in tiers — each tier adds a new data dimension without replacing the previous one:

| Tier    | Data Source                | What It Answers                               |
|---------|----------------------------|-----------------------------------------------|
| **0**   | Forge operational events   | "Is Forge working?"                           |
| **1**   | Forge compiler manifest    | "Which skills, versions, agents per project?" |
| **1.5** | OTLP metric export         | "Same data, in Grafana/DataDog"               |
| **2**   | Langfuse/Helicone import   | "Which skill costs how much?"                 |
| **3**   | Agent OTEL + file watchers | "Was this skill actually activated?"          |

## Integration Strategy

Ember integrates with existing tools rather than replacing them:

- **Langfuse** — correlate skill versions with LLM traces
- **Helicone** — enrich proxy logs with skill attribution
- **Grafana** — pre-built dashboard pack for Ember metrics
- **DataDog** — custom metrics with skill-version dimensions
- **Claude Code OTEL** — native event ingestion + version enrichment

## Related

- [Forge CLI](https://github.com/crucibledx/forge-cli) — AI environment config sync (the data source)
- [Forge Skills Starter](https://github.com/crucibledx/forge-skills-starter) — skill authoring template
- [crucibledx.dev/ember](https://crucibledx.dev/ember) — product page

## License

MIT
