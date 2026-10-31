# Contributing to KubeRoot

Thanks for your interest in KubeRoot! This document describes how we work.

## How changes are proposed

| Change size | What to do |
|-------------|-----------|
| Bug fix, small improvement | Open an issue (or go straight to a PR for trivial fixes) |
| New feature | Open an issue with acceptance criteria; wait for a maintainer to triage |
| Significant feature or cross-cutting change | Write a [design proposal](docs/proposals/0000-template.md) and get it merged before implementation |
| Architectural decision | Record it as an [ADR](docs/adr/0000-template.md) |

A change is "significant" if it adds a new subsystem, changes the Go<->UI contract in a
non-additive way, introduces a new dependency of note, or affects security or performance
characteristics.

## Development workflow

1. Fork the repo and create a short-lived branch from `main`.
2. Make your change with tests (`internal/` packages should be test-first where practical).
3. Run the checks locally:
   ```sh
   go vet ./... && go test -race ./...
   golangci-lint run
   cd frontend && npm run check
   ```
4. If you changed anything in `app/`, regenerate bindings (`wails3 generate bindings -ts -i`)
   and commit the result — CI fails if they are out of date.
5. Open a pull request. CI must pass and one maintainer must approve.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(resources): add CRD discovery to navigation tree
fix(logs): handle container restarts while following
docs: clarify kubeconfig search order
```

Common types: `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`, `chore`.
Changelogs and version bumps are generated from these.

## Developer Certificate of Origin (DCO)

All commits must be signed off, certifying you have the right to submit the work under the
project's license ([developercertificate.org](https://developercertificate.org/)):

```sh
git commit -s -m "feat: ..."
```

This adds a `Signed-off-by: Your Name <you@example.com>` trailer.

## Code style

- Go: `gofmt`/`goimports`, enforced by `golangci-lint` (see `.golangci.yml`).
- Keep `app/` services thin: validate input and delegate to `internal/`.
- Never block the UI thread: long-running work goes in goroutines and reports via events.
- Frontend: TypeScript strictness via `svelte-check`.

## Reporting security issues

Please do **not** open public issues for security problems. See [SECURITY.md](SECURITY.md).
