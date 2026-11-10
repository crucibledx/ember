# Ember v0.4 — Langfuse + Helicone Integration

> **Milestone:** Ember v0.4 — "Cost Attribution"
>
> **Prerequisite:** Ember v0.3 (OTLP export).
>
> **Scope:** Import LLM usage data from Langfuse/Helicone and correlate with
> manifest data. Answer: "Which skill costs how much?" This is Tier 2.
>
> **Effort:** ~2-3 weeks.

---

## Problem

Ember Tier 1 knows which skills are deployed and their estimated token cost. Langfuse/Helicone know actual LLM usage — tokens consumed, cost, latency. Neither alone can answer: "Did skill v2.1 cost 40% more than v2.0?"

## Solution

Import LLM trace data from Langfuse/Helicone APIs. Correlate by timestamp + project. Produce cost-attributed metrics per skill version.

### Tier 2a — Import-based (cheaper, first)

Read exported data from Langfuse/Helicone APIs. Correlate by timestamp + project:

- Langfuse says: "session abc123, 2400 tokens"
- Ember knows: "project compiled study-java (1200 token estimate) at that time"
- Ember infers: "study-java likely contributed to this session"

Approximate (time-based correlation), but useful for aggregate trends.

### Tier 2b — SDK-based (richer, optional, future)

Thin wrapper SDK (`@crucibledx/ember-sdk`) that tags LLM calls with skill context. Exact causal attribution. Separate package, separate spec.

## What Ships

1. **Langfuse importer** — read traces via Langfuse API
2. **Helicone importer** — read logs via Helicone API
3. **Correlator** — match LLM usage to skill versions by timestamp/project
4. **Enriched metrics** — actual_tokens, actual_cost alongside estimates
5. **Updated dashboards** — Grafana panels for cost attribution

## Config

```yaml
ember:
  integrations:
    langfuse:
      enabled: true
      base_url: "https://cloud.langfuse.com"
      public_key: "${LANGFUSE_PUBLIC_KEY}"
      secret_key: "${LANGFUSE_SECRET_KEY}"
    helicone:
      enabled: true
      api_key: "${HELICONE_API_KEY}"
```

## Correlation Strategy

Time-window matching: if a Langfuse trace occurred within N minutes of a manifest compile for the same project, attribute the trace's tokens to the compiled skills proportionally (by token estimate ratio).

Confidence field (`0-1`) on each correlation — dashboards can filter to high-confidence data only.

## Out of Scope

- Exact causal attribution SDK (separate spec)
- Agent OTEL receiver (v0.5)
- Real-time streaming from Langfuse/Helicone (batch import only)
