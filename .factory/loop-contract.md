# Loop contract

Read by the Crucible loop agents (`implement-spec`, `verifier`). Keep it accurate.

- **State:** Go module `github.com/crucibledx/ember`, a custom OpenTelemetry Collector distribution built from
  `builder-config.yaml`. Spec: `specs/0001-ember-v1.md`.
- **Toolchain:** Go 1.27.4 (`toolchain` in `go.mod`) and golangci-lint v2.15.0. The `Go` workflow pins the same
  versions with `GOTOOLCHAIN=local`; change them together.
- **Install:** none beyond Go; modules download on first build.
- **Checks** (all must pass before a push; CI runs the first four on ubuntu, macos and windows):
  `golangci-lint run`, `go vet ./...`, `go test -race ./...` (needs cgo), `go build ./...`.
  When `builder-config.yaml` or a collector dependency changes: `scripts/generate.sh`, then commit the changed
  `cmd/ember/components.go`, `go.mod` and `go.sum` (CI fails if they differ from the generator output).
  Release targets and the 80 MiB size gate: `scripts/build.sh` (five stripped binaries into `dist/`). Cross-builds
  fill the Go build cache by several GB; run `go clean -cache` afterwards on machines short of disk.
- **Commit style:** `chore: ...` / `docs: ...` until v0.1.0, then `feat(v0.x.y): ...` / `fix(v0.x.y): ...`.
- **Specs:** `specs/NNNN-slug.md`, see `crucibledx/hq` `specs/README.md`. Design drafts live in `docs/`.
- **Release:** manual. Loop PRs never tag or publish. The `Go` workflow's `workflow_dispatch` release build (binaries
  as workflow artifacts) is run by Nazar only; GoReleaser on a tag arrives in M3.
