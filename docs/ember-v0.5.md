# Ember v0.5 — Agent OTEL + File Watchers

> **Milestone:** Ember v0.5 — "Activation Detection"
>
> **Prerequisite:** Ember v0.3+ (OTLP infrastructure).
>
> **Scope:** Detect actual skill activation per agent. This is Tier 3 —
> confirming which skills were not just deployed, but actually used by the agent.
>
> **Effort:** ~4-6 weeks (incremental per agent).

---

## Problem

Tier 1-2 know which skills are deployed and their approximate cost. But "deployed" ≠ "used." A skill file might sit in `.claude/skills/` and never be referenced in a session. Knowing activation status is the highest-value signal.

## Agent-Specific Approaches

| Agent | Approach | Effort | Reliability |
|-------|---------|--------|-------------|
| Claude Code | Native OTEL (`claude_code.skill_activated`) | 2-3 weeks | High |
| Cline | Open source — read logs directly | 1-2 weeks | High |
| Cursor | Watch `.cursor/` directory + internal SQLite | 3-4 weeks | Medium |
| Windsurf | Watch `.windsurfrules` access | 2-3 weeks | Medium |
| Copilot | VS Code extension hook | 4-6 weeks | Low |

Build incrementally — Claude Code and Cline first (highest reliability), others follow.

## What Ships (incrementally)

1. **Claude Code OTEL receiver** — ingest `claude_code.skill_activated` events
2. **Cline log parser** — read Cline output logs for skill references
3. **Activation metrics** — activated/not-activated per skill per agent
4. **Updated dashboards** — activation rate panels

## Out of Scope (this version)

- Cursor, Windsurf, Copilot watchers (separate follow-up versions)
- Real-time activation streaming (batch only)
- Extraction to standalone `@crucibledx/ember` package (when needed)
