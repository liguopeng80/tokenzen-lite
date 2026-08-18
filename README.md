# Token Zen Lite

English | [简体中文](README.zh-CN.md)

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](server/go.mod)
[![Node](https://img.shields.io/badge/Node-18+-339933.svg)](package.json)

Self-hosted AI model API gateway with credit-based billing. Admins configure upstream
channels once, allocate credits to team members, and everyone calls every available
model with a single API key — the gateway meters each call and keeps a complete
usage and cost trail.

It replaces the usual per-key billing sprawl: instead of handing out provider API
keys (each with its own console, quota, and invoice), Token Zen Lite owns the
upstream keys, issues its own keys, and bills internally against admin-allocated
credit balances.

## Highlights

- **Multi-protocol relay.** Downstream endpoints follow the OpenAI and Anthropic
  request shapes (`/v1/chat/completions`, `/v1/messages`, `/v1/embeddings`,
  `/v1/images/generations`, `/v1/models`, plus `count_tokens`), with
  `/{provider}/v1/*` prefixed routes that pin a request to one provider. Upstream
  channels speak OpenAI-compatible, Anthropic, or Gemini protocols. Same-protocol
  traffic is passed through with model rewriting and usage sniffing; cross-protocol
  traffic is converted through a canonical intermediate model, streaming included.
- **Metered credit billing.** Per-model prices in credits per 1M tokens (image
  models per call) and time-of-day multipliers, computed in integer arithmetic.
  Every request is billed in three phases — precharge, settle (refund the
  difference, never below zero), refund on failure — with an idempotent ledger
  (`(request_id, entry_type)` unique index) as the single source of truth.
  `tzl reconcile` verifies balance == sum of ledger entries.
- **Channel management.** Multiple channels per model with priority routing and
  automatic failover; channels that fail repeatedly are auto-disabled and probed
  half-open before re-entering rotation.
- **Department cost attribution.** Flat departments as a cost dimension; usage
  logs and ledger entries snapshot the department at charge time, so reports stay
  stable when users move. Optional monthly budgets with over-budget alerts.
- **Admin and portal frontends.** Admin manages channels, model pricing, users,
  credits, departments, usage logs, cost reports, audit records, alerts, and
  settings. Portal gives members balance/usage views, self-managed API keys,
  redemption-code top-ups, and an integration guide; department heads get a
  department cost view.
- **Operations built in.** Prometheus metrics at `/metrics`, alert delivery via
  webhook and SMTP, append-only audit log enforced by database triggers, and an
  hourly maintenance pass (usage rollups, retention cleanup, low-balance checks,
  monthly allowance issuance, failure-rate and latency alerts).
- **Small operational footprint.** One Go binary plus PostgreSQL; no Redis.
  Rate limiting and caching are in-process, so deployment is single-instance.

Token Zen Lite is built as an internal team gateway, not a public SaaS: there is
no online payment, no public pricing page, no self-registration by default
(accounts are created by admins), and password resets are done by an admin.

## Components

| Component | Description |
|-----------|-------------|
| Backend `tzl` | Single Go binary: HTTP service, embedded SQL migrations, plus `reconcile` and `cleanup-precharge` subcommands |
| Admin frontend | React app for channels, model pricing, users and credits, departments, usage logs, cost reports, audit, alerts, settings |
| Portal frontend | React app for balance and usage, API key self-service, integration guide, redemption codes |
| PostgreSQL | The only external dependency (PostgreSQL 16) |

## Quick start with Docker

Requires Docker and Docker Compose.

```bash
git clone https://github.com/liguopeng80/tokenzen-lite.git
cd tokenzen-lite
cp .env.docker.example .env.docker
# Fill in per the comments: database password, session secret, encryption key
# (generate with: openssl rand -hex 32)
chmod 600 .env.docker
docker compose --env-file .env.docker up -d --build
```

The first start runs database migrations and creates the initial admin account.
If `TZL_ROOT_PASSWORD` is not set, a random password is printed to the log
exactly once:

```bash
docker compose logs backend | grep "初始 root"
```

Default entry points:

| Entry | Address | Notes |
|-------|---------|-------|
| Portal | `http://<host>:8080` | Also serves `/v1`; this is the base URL for client tools |
| Admin | `http://127.0.0.1:8081` | Bound to loopback by default; use an SSH tunnel or a reverse proxy with an IP allowlist for remote access (template: `deploy/nginx-admin-allowlist.conf.example`) |

Two things to know before going live:

- `TZL_ENCRYPT_KEY` is immutable once set. Upstream channel keys are encrypted
  with a key derived from it; lose it and every stored channel key becomes
  undecryptable. Store it off-machine, separately from database backups
  (`deploy/backup-secrets.sh`).
- The stack serves plain HTTP by default, so `TZL_SESSION_COOKIE_SECURE`
  defaults to `false`. After putting TLS in front, set it to `true` — otherwise
  browsers will drop the session cookie and logins will not persist.

## Quick start from source

Requires Go 1.25+, Node.js 18+, pnpm 8+, and PostgreSQL 16.

```bash
./setup.sh              # checks prerequisites, prepares .env and databases,
                        # runs migrations, installs deps, builds
bash scripts/start.sh   # backend + both frontend dev servers
bash scripts/status.sh  # status overview; --logs backend to follow logs
bash scripts/stop.sh
```

Dev ports: backend 19030, admin 19073, portal 19074.

## First-run configuration

The admin dashboard lists the remaining setup steps on a fresh install; the
short version:

1. Log in to admin with the initial root account and change the password.
2. **Channels**: add at least one upstream channel (provider, protocol, base
   URL, upstream API key, list of model names).
3. **Models → bulk import**: import the built-in preset price list (vendor
   public prices at collection time — verify against current vendor pricing),
   apply a markup percentage, review the resulting credit prices, and import.
4. **Settings**: confirm the exchange rate (default: 1 CNY = 1,000,000 credits)
   and the public API base URL used in the portal's integration guide.
5. **Users**: create accounts and allocate credits (or hand out redemption
   codes). Passwords may be left blank; a one-time initial password is generated
   and shown once, and the user must change it at first login.
6. Users log in to the portal, create API keys, and configure their clients
   with the base URL from the integration guide.

## Architecture

Backend packages under `server/internal/`, dependencies flowing strictly
downward (details in [`docs/overview/architecture.md`](docs/overview/architecture.md)):

| Layer | Packages | Responsibility |
|-------|----------|----------------|
| Transport | `api` | HTTP routing, middleware, handlers |
| Runtime | `relay`, `maintenance` | Relay core (conduit per downstream × upstream protocol pair); hourly background jobs |
| Domain services | `billing`, `alerting`, `audit`, `auth` | Ledger-backed balance adjustments; alert dedup and delivery; append-only audit |
| Compute / persistence | `pricing`, `store` | Pure-function credit math; GORM repositories, embedded SQL migrations |
| Foundation | `domain`, `obs`, `config`, `secrets`, `ratelimit`, `strutil` | Enums, structured logging and metrics, env config, AES-GCM keybox, in-memory rate limiting |

Frontends live in a pnpm workspace: `apps/admin`, `apps/portal`, and
`packages/shared` (shared types and constants, kept in sync with
`server/internal/domain`).

## Configuration

All configuration is via environment variables; see
[`.env.example`](.env.example) (source/dev) and
[`.env.docker.example`](.env.docker.example) (Docker Compose) for the full list
with comments. Production-critical items:

| Variable | Purpose |
|----------|---------|
| `TZL_DATABASE_URL` | PostgreSQL connection string (required) |
| `TZL_SESSION_SECRET` | Session cookie signing key, 32+ characters (required in prod) |
| `TZL_ENCRYPT_KEY` | Encryption key for stored upstream channel keys; immutable once set |
| `TZL_ROOT_USERNAME` / `TZL_ROOT_PASSWORD` | Initial admin bootstrap (random password logged if empty) |
| `TZL_METRICS_TOKEN` | Bearer token for `/metrics` (root session accepted when unset) |
| `TZL_TRUSTED_PROXIES` | CIDR list of proxies whose `X-Real-IP` is trusted |

## Operations

| Task | How |
|------|-----|
| Credit reconciliation (daily recommended) | `tzl reconcile` — non-zero exit on balance/ledger mismatch or orphaned precharges |
| Orphaned precharge cleanup | `tzl cleanup-precharge [minutes]` |
| Database backup / restore | `deploy/backup.sh`, `deploy/backup-secrets.sh`, `deploy/restore.sh` |
| Metrics scraping | `GET /metrics` (Prometheus text format) |
| Non-container deployment | systemd + Nginx: [`docs/deployment.md`](docs/deployment.md), templates in `deploy/` |

## Development

```bash
make test    # Go tests (needs TZL_TEST_DATABASE_URL) + frontend type-check and unit tests
make build   # Go binary + frontend builds
```

Notes:

- Go integration tests share one test database and run migrations up/down, so
  packages must run serially: `go test -p 1 ./...` from `server/`. Tests skip
  automatically when `TZL_TEST_DATABASE_URL` is unset.
- `make test` includes a coverage floor on `server/internal/api`
  (`scripts/coverage-gate.sh`, currently 70%) — new endpoints need test cases.
- CI (`.github/workflows/ci.yml`) runs `go vet`, gofmt check, the full backend
  suite against a PostgreSQL service container, the coverage gate, frontend
  type-check/tests/build, and both container image builds.
- `mock-upstream/` contains a mock API server for exercising the relay without
  real providers.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and conventions.

## Documentation

Reference documentation in [`docs/`](docs/) is currently written in Chinese:

| Document | Contents |
|----------|----------|
| [`docs/glossary.md`](docs/glossary.md) | Authoritative terms, enum values, settings keys |
| [`docs/api-contract.md`](docs/api-contract.md) | Full HTTP API contract |
| [`docs/overview/architecture.md`](docs/overview/architecture.md) | Layered architecture and module inventory |
| [`docs/deployment.md`](docs/deployment.md) | Deployment, backup/restore, security baseline, memory budget |
| [`docs/design/组织与审计模型.md`](docs/design/组织与审计模型.md) | Data model for departments and audit |

## License

[Apache-2.0](LICENSE). Derivative distributions must retain the
[LICENSE](LICENSE) and [NOTICE](NOTICE) files and carry prominent
notices stating changes made to the original files (Apache-2.0, Section 4).
