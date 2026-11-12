# Ember: agent guide

How to work in this repo and where to find things. Ember is in the design phase: there is no code yet.

## Where to find information

| Need                                         | Read                                                                                                            |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Current state, checks, commit style, release | [`.factory/loop-contract.md`](.factory/loop-contract.md)                                                        |
| Org context, product status, delivery loop   | `crucibledx/hq` `AGENTS.md`                                                                                     |
| Ember research (reference, may be stale)     | `crucibledx/hq` `research/*ember*.md`, `research/crucible-observability-product.md`                             |
| Cross-project architecture (source of truth) | `crucibledx/hq` `design/`                                                                                       |
| Diagrams                                     | `crucibledx/assets` `ember/diagrams/`                                                                           |
| Spec process                                 | [`specs/README.md`](specs/README.md)                                                                            |
| Org engineering standards                    | `engineering-standards` skill (`crucibledx/factory-loop` `plugins/crucible-loop/skills/engineering-standards/`) |

## How to work

- Nothing is approved product direction until a spec merges, here in `specs/` or in `crucibledx/hq` `specs/`.
  Research notes, roadmaps and unmerged drafts (including the Forge manifest Ember plans to read) are input, not
  decisions.
- Until a spec approves code, PRs are docs or specs only.
- When code arrives, follow the loop contract's checks and forge-cli's test isolation: never run raw `bun test`.

## Boundaries

- Never push to `main`, force push or merge. Nazar's code-owner approval is the merge gate.
- No tags or publishing in agent PRs; releases are manual.
