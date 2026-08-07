# Contributing to Kaku

## Branches

```
feature branch  →  dev  →  main  →  tag v*
```

- **`main`** is release-only. Every commit on it is a release candidate; tags `v*` trigger goreleaser
  and the container publish. Do not push to it directly.
- **`dev`** is where features integrate. PRs target `dev`.
- **feature branches** are named `feat/<thing>`, `fix/<thing>`, or `chore/<thing>` and branch from `dev`.
  One feature per branch, squash-merged.

Cutting a release: PR `dev` → `main`, merge, then `git tag vX.Y.Z && git push --tags`.

## Setup

```sh
make dev     # fetches the pinned sqlc and Tailwind binaries into bin/, then runs everything
```

Go 1.26+ is the only prerequisite. `templ` comes from `go tool`; `sqlc` and the Tailwind standalone CLI
are pinned versions the Makefile downloads. There is no Node dependency.

## Generated code is committed

`*_templ.go` and `internal/web/static/tailwind.css` are checked in so a clean checkout builds without
any toolchain. Run `make generate` before committing; CI fails the build if they are stale.

## House style

Kaku is deliberately small. Before adding code, in order:

1. Does this need to exist? Speculative need is not a reason.
2. Is it already in the codebase? Reuse it.
3. Does the standard library do it?
4. Does an already-installed dependency do it?

New dependencies need a reason a few lines of Go could not cover. No interfaces with one
implementation, no config for values that never change, no scaffolding for later.

Non-trivial logic ships with one runnable test — the smallest thing that fails if the logic breaks.
Standard `testing` only; no frameworks, no fixtures.

Never simplify away: input validation at trust boundaries, HTML sanitising, auth checks, or
accessibility basics.

## Checks

```sh
make lint    # go vet + gofmt
make test
```

CI runs both plus a Docker build on every PR.
