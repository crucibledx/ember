# Loop contract

Read by the Crucible loop agents (`implement-spec`, `verifier`). Keep it accurate.

- **State:** design phase. There is no `package.json` or `src/` yet.
- **Install:** none yet. Once code exists: `bun install`.
- **Checks:** none yet; docs PRs need only accurate links. Once code exists, mirror forge-cli:
  `bun run format:check && bun run lint && bun run typecheck && bun test && bun run build`.
- **Commit style:** `docs: ...` until the first release, then `feat(v0.x.y): ...` / `fix(v0.x.y): ...`.
- **Specs:** `specs/NNNN-slug.md`, see `crucibledx/hq` `specs/README.md`. Design drafts live in `docs/`.
- **Release:** manual. Loop PRs never tag or publish.
