# Git workflow

This project uses **Git Flow** for branching, **Conventional Commits** for commit
messages, and **Semantic Versioning** for releases. The goal is a history that reads
cleanly, releases that are reproducible from tags, and a CHANGELOG that writes itself
from the commit log.

## Branches

| Branch        | Lives for | Branches from | Merges into        | Purpose                                  |
|---------------|-----------|---------------|--------------------|------------------------------------------|
| `main`        | forever   | —             | —                  | Released, production-ready code only. Every commit is tagged. |
| `develop`     | forever   | `main`        | —                  | Integration branch; the next release accumulates here. |
| `feature/*`   | short     | `develop`     | `develop`          | A unit of work (e.g. `feature/runtime-core`). |
| `release/*`   | short     | `develop`     | `main` + `develop` | Stabilize a version (e.g. `release/0.1.0`): bump CHANGELOG, fix last bugs. |
| `hotfix/*`    | short     | `main`        | `main` + `develop` | Urgent fix to a released version (e.g. `hotfix/0.1.1`). |

Merges into `develop` and `main` use `--no-ff` so each feature/release is a visible
unit in the graph.

## Commit messages (Conventional Commits)

```
<type>(<optional scope>): <subject>

<optional body — what & why, not how>

<optional footer — BREAKING CHANGE:, Refs: #123>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`,
`chore`, `revert`. Scope is a package or area, e.g. `runtime`, `manifest`, `examples`.

- `feat:` → minor version bump. `fix:` → patch bump. `BREAKING CHANGE:` → major bump.
- Subject: imperative mood, lower-case, no trailing period, ≤ ~72 chars.

## Versioning & tags

Semantic Versioning `vMAJOR.MINOR.PATCH`. Pre-1.0 (`0.x`) the minor slot absorbs
breaking changes. Tags are annotated and live on `main`:

```bash
git tag -a v0.1.0 -m "v0.1.0 — runtime core"
```

## Release procedure

```bash
# 1. cut a release branch from develop
git switch develop && git switch -c release/0.1.0

# 2. move CHANGELOG [Unreleased] entries under a new [0.1.0] heading, finalize
git commit -am "chore(release): prepare v0.1.0"

# 3. merge to main and tag
git switch main && git merge --no-ff release/0.1.0 -m "release: v0.1.0"
git tag -a v0.1.0 -m "v0.1.0 — runtime core"

# 4. merge back into develop so it carries the release commit
git switch develop && git merge --no-ff release/0.1.0 -m "Merge release/0.1.0 into develop"
git branch -d release/0.1.0
```

## Changelog

Maintained in [`CHANGELOG.md`](../CHANGELOG.md) following *Keep a Changelog*. New work
is recorded under `## [Unreleased]` as it merges to `develop`, then promoted to a
versioned section during the release procedure.
