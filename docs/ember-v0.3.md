# Ember v0.3 — OTLP Export + Grafana Dashboard Pack

> **Milestone:** Ember v0.3 — "Push to Grafana"
>
> **Prerequisite:** Ember v0.2 (aggregation).
>
> **Scope:** Push Ember metrics to any OTLP-compatible backend. Ship pre-built
> Grafana dashboard JSONs for instant visualization. This is when Ember becomes
> useful for teams that already have Grafana/DataDog.
>
> **Effort:** ~1-2 weeks.

---

## Problem

CLI reports are useful for developers. Engineering managers and platform teams want dashboards — Grafana, DataDog, or their existing observability stack. Manual JSON → dashboard import doesn't scale.

## Solution

1. **OTLP metric push** — export Ember metrics as OpenTelemetry metrics to any OTLP endpoint
2. **Grafana dashboard pack** — pre-built JSON dashboards, import and go
3. **Config** — OTLP endpoint + auth in forge config

## What Ships

1. **OTLP exporter** — push metrics using `@opentelemetry/exporter-metrics-otlp-http`
2. **`forge ember --push`** — one-shot push of current metrics
3. **Grafana dashboards** — 4 JSON files ready to import
4. **Config schema** — `ember.otlp` section in forge config

## Config

```yaml
ember:
  enabled: true
  otlp:
    endpoint: "http://localhost:4318"
    headers:
      Authorization: "Bearer ${GRAFANA_TOKEN}"
```

## OTLP Metrics

Metric names following OpenTelemetry semantic conventions:

| Metric | Type | Labels |
|--------|------|--------|
| `crucible.ember.skills.deployed` | Gauge | project, agent |
| `crucible.ember.skills.shaken` | Gauge | project |
| `crucible.ember.tokens.total` | Gauge | project, agent |
| `crucible.ember.tokens.budget` | Gauge | project, agent |
| `crucible.ember.budget.utilization` | Gauge | project, agent |
| `crucible.ember.compile.duration_ms` | Gauge | project |
| `crucible.ember.version.drift` | Gauge | skill_id |
| `crucible.ember.version.adoption` | Gauge | skill_id, version |

## Grafana Dashboard Pack

```
dashboards/
  overview.json        — Skills deployment overview across projects
  drift.json           — Version drift heatmap
  tokens.json          — Token budget utilization per agent
  adoption.json        — Skill version adoption rates
  README.md            — Import instructions
```

No Grafana plugin needed. Standard dashboard import, works with any Grafana instance 9.0+.

## File Location

```
domain/ember/
  otlp.ts           — formatMetricsForOTLP

infrastructure/ember/
  exporter.ts       — pushToOTLP (HTTP client)

dashboards/          — Grafana JSON files (shipped with Ember)
```

## Tests

- Format metrics → valid OTLP payload
- Push to mock OTLP endpoint → correct HTTP request
- Missing endpoint config → helpful error
- Invalid endpoint → retry with backoff, then error
- Dashboard JSONs are valid Grafana dashboard schema

## Out of Scope

- Langfuse/Helicone integration (v0.4)
- Agent OTEL receiver (v0.5+)
- Watch mode / continuous push (v0.5+)
