#!/usr/bin/env bash
# One-command bootstrap for local development.
# Checks prerequisites, prepares .env and databases, installs frontend
# dependencies, runs database migrations, and builds backend + frontends.
# Safe to re-run: every step is idempotent.
#
# For a containerized setup instead, see README.md ("Quick start with Docker")
# and .env.docker.example.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

say()  { printf '\n==> %s\n' "$*"; }
die()  { printf '\n[setup] ERROR: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# version_ge A B — succeed if version A >= B (numeric, dot-separated)
version_ge() {
  local IFS=. a b i
  a=($1) b=($2)
  for i in 0 1 2; do
    local x=${a[i]:-0} y=${b[i]:-0}
    x=${x%%[!0-9]*}; y=${y%%[!0-9]*}; x=${x:-0}; y=${y:-0}
    (( x > y )) && return 0
    (( x < y )) && return 1
  done
  return 0
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$2"
}

# ---------------------------------------------------------------------------
# 1. Prerequisites
# ---------------------------------------------------------------------------

say "Checking prerequisites"

require_cmd go "Go toolchain not found. Install Go (see https://go.dev/dl/), then re-run ./setup.sh."

GO_REQUIRED="$(awk '/^go /{print $2}' server/go.mod | cut -d. -f1-2).0"
GO_INSTALLED="$(go version | awk '{print $3}' | sed 's/^go//' | cut -d. -f1-2).0"
version_ge "$GO_INSTALLED" "$GO_REQUIRED" \
  || die "Go ${GO_REQUIRED%.*}+ is required (server/go.mod), found $(go version | awk '{print $3}')."

require_cmd node "Node.js not found. Install Node.js 18+ (see https://nodejs.org/), then re-run ./setup.sh."
NODE_INSTALLED="$(node --version | sed 's/^v//')"
version_ge "$NODE_INSTALLED" 18.0.0 \
  || die "Node.js 18+ is required, found ${NODE_INSTALLED}."

if ! command -v pnpm >/dev/null 2>&1; then
  die "pnpm not found. Enable corepack (corepack enable) or run: npm install -g pnpm"
fi
PNPM_INSTALLED="$(pnpm --version)"
version_ge "$PNPM_INSTALLED" 8.0.0 \
  || die "pnpm 8+ is required, found ${PNPM_INSTALLED}."

require_cmd psql "psql not found. Install the PostgreSQL client (postgresql-client / postgresql), then re-run ./setup.sh."

printf 'OK: go %s, node %s, pnpm %s\n' "$GO_INSTALLED" "$NODE_INSTALLED" "$PNPM_INSTALLED"

# ---------------------------------------------------------------------------
# 2. Environment file
# ---------------------------------------------------------------------------

say "Preparing environment file"

if [ ! -f .env ]; then
  cp .env.example .env
  printf 'Created .env from .env.example (defaults target a local PostgreSQL on port 5433).\n'
else
  printf '.env already exists, keeping it.\n'
fi

printf 'If your PostgreSQL differs from the defaults, edit TZL_DATABASE_URL in .env now,\nthen re-run ./setup.sh. Docker users: use .env.docker.example instead (see README.md).\n'

# ---------------------------------------------------------------------------
# 3. Database
# ---------------------------------------------------------------------------

say "Checking database"

env_value() {
  grep -E "^$1=" .env | head -1 | cut -d= -f2- | sed 's/^"//;s/"$//;s/^'"'"'//;s/'"'"'$//'
}

DB_URL="$(env_value TZL_DATABASE_URL)"
[ -n "$DB_URL" ] || die "TZL_DATABASE_URL is empty in .env — set it to your PostgreSQL connection string."

if ! [[ "$DB_URL" =~ ^postgres://([^:/@]+):([^@/]+)@([^:/]+):([0-9]+)/([^?]+) ]]; then
  die "Cannot parse TZL_DATABASE_URL in .env. Expected format: postgres://user:password@host:port/dbname"
fi
DB_USER="${BASH_REMATCH[1]}"; DB_PASS="${BASH_REMATCH[2]}"
DB_HOST="${BASH_REMATCH[3]}"; DB_PORT="${BASH_REMATCH[4]}"; DB_NAME="${BASH_REMATCH[5]}"

if ! pg_isready -h "$DB_HOST" -p "$DB_PORT" >/dev/null 2>&1; then
  die "PostgreSQL is not reachable at ${DB_HOST}:${DB_PORT}.
  Start your PostgreSQL server, or point TZL_DATABASE_URL in .env at a
  running instance, then re-run ./setup.sh."
fi
printf 'PostgreSQL reachable at %s:%s\n' "$DB_HOST" "$DB_PORT"

# create_db USER PASS HOST PORT NAME — create database NAME if missing
create_db() {
  local user="$1" pass="$2" host="$3" port="$4" name="$5"
  if PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d "$name" -tAc 'SELECT 1' >/dev/null 2>&1; then
    printf 'Database %s already exists.\n' "$name"
    return 0
  fi
  if ! PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d postgres -tAc 'SELECT 1' >/dev/null 2>&1; then
    die "Cannot connect as ${user} to database 'postgres' on ${host}:${port}.
  Create the role and databases manually (as a PostgreSQL superuser):

      CREATE ROLE ${user} LOGIN PASSWORD '<your-password>';
      CREATE DATABASE ${name} OWNER ${user};

  Then re-run ./setup.sh."
  fi
  PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d postgres \
    -c "CREATE DATABASE \"$name\" OWNER \"$user\"" >/dev/null
  printf 'Database %s created.\n' "$name"
}

create_db "$DB_USER" "$DB_PASS" "$DB_HOST" "$DB_PORT" "$DB_NAME"

# Test database for `make test` (integration tests share one database and are
# therefore run serially with -p 1). Defaults to the same server as the dev
# database unless TZL_TEST_DATABASE_URL points elsewhere.
TEST_DB_URL="$(env_value TZL_TEST_DATABASE_URL)"
if [ -n "$TEST_DB_URL" ]; then
  if [[ "$TEST_DB_URL" =~ ^postgres://([^:/@]+):([^@/]+)@([^:/]+):([0-9]+)/([^?]+) ]]; then
    create_db "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[4]}" "${BASH_REMATCH[5]}"
  else
    printf 'Warning: cannot parse TZL_TEST_DATABASE_URL in .env — test database not checked.\n'
  fi
else
  create_db "$DB_USER" "$DB_PASS" "$DB_HOST" "$DB_PORT" tzl_test
fi

# ---------------------------------------------------------------------------
# 4. Migrations
# ---------------------------------------------------------------------------

say "Running database migrations"

if (cd server && TZL_DATABASE_URL="$DB_URL" go run ./cmd/tzl migrate up); then
  printf 'Migrations applied (the server also applies pending migrations on startup).\n'
else
  die "Migration failed. Check TZL_DATABASE_URL in .env and PostgreSQL logs."
fi

# ---------------------------------------------------------------------------
# 5. Frontend dependencies
# ---------------------------------------------------------------------------

say "Installing frontend dependencies"

pnpm install

# ---------------------------------------------------------------------------
# 6. Build
# ---------------------------------------------------------------------------

say "Building backend and frontends"

make build

# ---------------------------------------------------------------------------
# 7. Next steps
# ---------------------------------------------------------------------------

cat <<'EOF'

Setup complete.

Next steps:

  1. Start everything (backend + both frontend dev servers):

       bash scripts/start.sh

     URLs: backend http://localhost:19030
           admin   http://localhost:19073
           portal  http://localhost:19074

     Logs: bash scripts/status.sh --logs backend   (also admin | portal)
     Stop: bash scripts/stop.sh

  2. Initial admin account: on first start the server creates the root user
     and prints a random password to the backend log exactly once — search for
     the line containing "初始 root". To choose the password yourself, set
     TZL_ROOT_PASSWORD in .env before first start.

  3. Run the test suite (integration tests need the tzl_test database):

       make test

     See README.md and CONTRIBUTING.md for details.
EOF
