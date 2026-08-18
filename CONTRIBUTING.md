# Contributing to Token Zen Lite

Thanks for your interest in contributing.

## Setting up

```bash
git clone https://github.com/liguopeng80/tokenzen-lite.git
cd tokenzen-lite
./setup.sh
```

`setup.sh` checks prerequisites (Go 1.25+, Node.js 18+, pnpm 8+, PostgreSQL 16),
creates `.env`, creates the dev/test databases, runs migrations, installs
frontend dependencies, and builds everything. For a containerized environment
instead, follow the Docker quick start in [README.md](README.md).

Daily workflow:

```bash
bash scripts/start.sh    # backend + frontend dev servers
bash scripts/stop.sh
make test                # full test suite
make build               # Go binary + frontend builds
```

## Testing expectations

- Backend integration tests share a single test database and recreate schema
  via migrations, so Go test packages must run serially: `go test -p 1 ./...`
  from `server/`. Tests skip automatically when `TZL_TEST_DATABASE_URL` is
  unset.
- `make test` runs the Go suite, a coverage floor on `server/internal/api`
  (`scripts/coverage-gate.sh`, currently 70%), and the frontend type-check and
  unit tests. New endpoints require accompanying test cases, or the gate fails.
- Changes touching billing must also run the `internal/billing` and
  `internal/api` (relay scenarios) tests, plus `go run ./cmd/tzl reconcile`.
- CI (`.github/workflows/ci.yml`) enforces `go vet`, gofmt, the full backend
  suite, the coverage gate, frontend checks, and image builds on every PR.

## Code conventions

- Go code follows gofmt; run `gofmt -w` on changed files.
- Business enums live in `server/internal/domain` and must stay in sync with
  `packages/shared/src/constants`. When adding an enum, update
  [`docs/glossary.md`](docs/glossary.md) first — it is the authoritative
  definition.
- The codebase's comments and documentation are written in Chinese; please
  match the existing style of the files you touch.

## Commit messages

This repository uses [Conventional Commits](https://www.conventionalcommits.org/):
`type(scope): subject`, e.g. `feat(admin): ...`, `fix(relay): ...`,
`docs(deployment): ...`. Common types: `feat`, `fix`, `docs`, `chore`, `refactor`,
`test`.

## Pull requests

- Keep PRs focused; one logical change per PR.
- Run `make test` before pushing and make sure CI is green.
- Describe what changed and why; for behavior changes, note how you verified
  them (tests added, manual verification steps).
- New features that add endpoints or billing behavior must come with tests —
  the coverage gate will catch the former, review will ask for the latter.

## Contributor license

By submitting a PR you accept the [CLA](.github/CLA.md). You keep the full copyright of
your contribution; the CLA grants the project owner a perpetual, royalty-free,
sublicensable license — including the right to relicense your contribution
under different terms (e.g. commercial licensing). The community's Apache-2.0
rights to your contribution are unaffected.

## Reporting issues

Use the issue templates (bug report / feature request) and include the
environment details they ask for — deployment mode (source vs Docker Compose),
Go and PostgreSQL versions, and relevant logs.
