# Ember v0.2 — Multi-Project Discovery + Aggregation

> **Milestone:** Ember v0.2 — "Fleet View"
>
> **Prerequisite:** Ember v0.1 (single manifest reader).
>
> **Scope:** Discover manifests across multiple projects, aggregate metrics,
> compute version drift and adoption rates. The first "enterprise-useful" view.
>
> **Effort:** ~1 week.

---

## Problem

v0.1 reads one manifest. A team has 20 projects. The questions that matter are cross-project: "Are all projects on the latest skill version?" "What's our total token spend?" "Which skills are never used?"

## Solution

1. **Discovery** — recursively find all `.crucible/forge.manifest.json` files under a directory
2. **Aggregation** — compute cross-project metrics from all discovered manifests
3. **CLI report** — `forge ember --dir ~/projects` shows fleet-wide view

## What Ships

1. **Manifest discovery** — walk directory tree, find all manifests
2. **Cross-project aggregation** — drift, adoption, totals
3. **Fleet report** — extended CLI output for multi-project
4. **JSON export** — `forge ember --dir ~/projects --json` for automation

## Cross-Project Metrics

| Metric | Derivation |
|--------|-----------|
| Projects discovered | Count of manifests found |
| Version drift per skill | Compare `skill.version` across all manifests |
| Adoption rate per version | % of projects with version X of skill Y |
| Total token budget across fleet | Sum `total_tokens` across all manifests |
| Tree-shaken skills (fleet-wide) | Skills appearing in `tree_shaken` in >80% of projects → deprecation candidates |
| Agent distribution | Count agents across all projects |
| Stale compiles | Projects where `compiled_at` is older than threshold |

## CLI Output (fleet mode)

```
$ forge ember --dir ~/projects

  Ember Fleet Report — 18 projects discovered
  ────────────────────────────────────────────
  Last scan: 2026-09-14T18:00:00Z

  Version Drift
    study-java     v2.1.0 (14 projects)  v2.0.0 (3 projects)  v1.9.0 (1 project)
    review-java    v1.5.0 (18 projects)  ✓ fully adopted
    detect-project v3.0.0 (16 projects)  v2.8.0 (2 projects)

  Deprecation Candidates (tree-shaken in >80% of projects)
    python-lint         shaken in 16/18 projects
    rust-conventions    shaken in 17/18 projects

  Fleet Totals
    Agent         Projects  Avg Tokens  Total Budget
    claude-code         18      8,200     147,600
    cursor              12      6,800      81,600

  Stale Compiles (>7 days since last sync)
    legacy-api       last compile: 2026-09-01 (13 days ago)
```

## File Location

```
domain/ember/
  discovery.ts    — findManifests, walkDirectory
  aggregator.ts   — aggregateMetrics, computeDrift, computeAdoption
```

## Tests

- Discover manifests in nested directory structure
- Empty directory → "No manifests found"
- Aggregation with uniform versions → "fully adopted"
- Aggregation with mixed versions → drift report
- Stale compile detection with configurable threshold
- Tree-shaken frequency computation

## Out of Scope

- OTLP export (v0.3)
- Grafana dashboards (v0.3)
- External integrations (v0.4+)
