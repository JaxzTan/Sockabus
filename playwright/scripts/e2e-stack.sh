#!/usr/bin/env bash
# Throwaway stack for the Playwright suite (playwright/README.md). Never touches the Compose `app`/`web`
# containers or the database in .env (Supabase): it runs its own PostgreSQL, Redis and Temporal
# containers on other ports, the Go API from source on :18080 and the console (`pnpm dev`) on :13000.
#
#   playwright/scripts/e2e-stack.sh up       start everything, migrate + seed the scratch DB, wait until ready
#   playwright/scripts/e2e-stack.sh test     run the suite against the stack (extra args go to playwright)
#   playwright/scripts/e2e-stack.sh down     stop the API and console, remove the three containers and their data
#   playwright/scripts/e2e-stack.sh status   what is running
#   playwright/scripts/e2e-stack.sh env      print the test variables (source it: eval "$(playwright/scripts/e2e-stack.sh env)")
#
# LLM: the provider and models in .env (anthropic = Claude by default), so persona generation and replies are
# real model output. E2E_LLM_PROVIDER=fake forces the offline fake. Only these keys are read from
# .env; DATABASE_URL / SOCKABUS_POSTGRES_URL from .env are never used.
set -euo pipefail

PW=$(cd "$(dirname "$0")/.." && pwd)   # playwright/
ROOT=$(cd "$PW/.." && pwd)            # repo root
STATE=${E2E_STATE_DIR:-$PW/.e2e}
NAME=sockabus-e2e
PG_PORT=${E2E_PG_PORT:-15432}
REDIS_PORT=${E2E_REDIS_PORT:-16379}
TEMPORAL_PORT=${E2E_TEMPORAL_PORT:-17233}
API_PORT=${E2E_API_PORT:-18080}
WEB_PORT=${E2E_WEB_PORT:-13000}
DB_NAME=sockabus_e2e
SCRATCH_DB="postgres://buru:buru@localhost:${PG_PORT}/${DB_NAME}?sslmode=disable"

die() { echo "e2e-stack: $*" >&2; exit 1; }
log() { echo "e2e-stack: $*"; }

# Hard safety rule: the suite writes append-only rows, so it must never reach Supabase (production).
guard_db() {
  local v
  for v in "${DATABASE_URL:-}" "${SOCKABUS_POSTGRES_URL:-}" "$SCRATCH_DB"; do
    case "$v" in *supabase*) die "refusing to run: a database URL points at Supabase (production). Unset DATABASE_URL / SOCKABUS_POSTGRES_URL." ;; esac
  done
  case "$SCRATCH_DB" in *localhost:${PG_PORT}/${DB_NAME}*) ;; *) die "scratch DB URL is not the local e2e container" ;; esac
}

# One key from .env, without sourcing the file (it also holds the production DATABASE_URL).
dotenv() {
  [ -f "$ROOT/.env" ] || return 0
  grep -E "^$1=" "$ROOT/.env" | tail -n1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//"
}

wait_for() { # wait_for <seconds> <description> <command...>
  local secs=$1 what=$2; shift 2
  for _ in $(seq 1 "$secs"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  die "$what not ready after ${secs}s"
}

container() { # container <suffix> <docker run args...>
  local n="$NAME-$1"; shift
  if [ -n "$(docker ps -aq -f "name=^${n}$")" ]; then
    docker start "$n" >/dev/null
  else
    docker run -d --name "$n" --label sockabus-e2e=1 "$@" >/dev/null
  fi
}

running() { [ -f "$1" ] && kill -0 "$(cat "$1")" 2>/dev/null; }

up() {
  guard_db
  mkdir -p "$STATE/telegram"
  command -v docker >/dev/null || die "docker is required"
  command -v migrate >/dev/null || die "golang-migrate is required (go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1)"

  # Secrets for this stack only, kept across `up` calls until `down`.
  if [ ! -f "$STATE/stack.env" ]; then
    umask 077
    cat > "$STATE/stack.env" <<EOF
SOCKABUS_API_URL=http://localhost:${API_PORT}
SOCKABUS_WEB_URL=http://localhost:${WEB_PORT}
SOCKABUS_OPERATOR_TOKEN=$(openssl rand -hex 24)
SOCKABUS_MESSAGE_KEY=$(openssl rand -base64 32)
SOCKABUS_CONSOLE_USER=e2e-operator
SOCKABUS_CONSOLE_PASSWORD=$(openssl rand -hex 16)
EOF
  fi
  # shellcheck disable=SC1091
  . "$STATE/stack.env"

  log "starting PostgreSQL :$PG_PORT, Redis :$REDIS_PORT, Temporal :$TEMPORAL_PORT"
  container pg -p "${PG_PORT}:5432" -e POSTGRES_USER=buru -e POSTGRES_PASSWORD=buru -e POSTGRES_DB=$DB_NAME postgres:16-alpine
  container redis -p "${REDIS_PORT}:6379" redis:7.4-alpine
  container temporal -p "${TEMPORAL_PORT}:7233" temporalio/temporal:1.5.1 server start-dev --ip 0.0.0.0
  wait_for 60 "PostgreSQL" docker exec "$NAME-pg" pg_isready -U buru -d $DB_NAME
  wait_for 30 "PostgreSQL connections" docker exec "$NAME-pg" psql -U buru -d $DB_NAME -c 'select 1'

  log "migrating and seeding $DB_NAME"
  migrate -path "$ROOT/db/migrations" -database "$SCRATCH_DB" up
  docker exec -i "$NAME-pg" psql -v ON_ERROR_STOP=1 -q -U buru -d $DB_NAME < "$ROOT/db/seed/001_demo.sql"

  local provider=${E2E_LLM_PROVIDER:-$(dotenv SOCKABUS_LLM_PROVIDER)}
  provider=${provider:-anthropic}
  if [ "$provider" = anthropic ] || [ "$provider" = claude ]; then
    local akey amodel acode
    akey=$(dotenv ANTHROPIC_API_KEY)
    amodel=$(dotenv SOCKABUS_EXTRACT_MODEL); amodel=${amodel:-claude-haiku-5-5}
    if [ -z "$akey" ]; then
      log "ANTHROPIC_API_KEY missing in .env: falling back to SOCKABUS_LLM_PROVIDER=fake"
      provider=fake
    else
      # One tiny request: is the key usable right now? The key goes in a header, never on the command line.
      acode=$(curl -s -o "$STATE/llm-probe.json" -w '%{http_code}' --max-time 60 -K - \
        -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \
        -d "{\"model\":\"${amodel}\",\"max_tokens\":2048,\"messages\":[{\"role\":\"user\",\"content\":\"reply with ok\"}]}" \
        https://api.anthropic.com/v1/messages <<<"header = \"x-api-key: ${akey}\"" || echo 000)
      if [ "$acode" != 200 ]; then
        log "WARNING: Claude ($amodel) answered HTTP $acode: $(grep -o '"message": *"[^"]*' "$STATE/llm-probe.json" | head -1 | cut -c1-200)"
        log "WARNING: the real LLM is not usable: falling back to SOCKABUS_LLM_PROVIDER=fake (canned, offline replies)"
        provider=fake
      fi
      rm -f "$STATE/llm-probe.json"
    fi
  fi
  if [ "$provider" = gemini ]; then
    local key model code
    key=$(dotenv GEMINI_API_KEY)
    model=$(dotenv SOCKABUS_PERSONA_MODEL); model=${model:-gemini-3.5-flash}
    if [ -z "$key" ]; then
      log "GEMINI_API_KEY missing in .env: falling back to SOCKABUS_LLM_PROVIDER=fake"
      provider=fake
    else
      # One tiny request: is the key usable right now (not out of quota)? The key goes in a header.
      code=$(curl -s -o "$STATE/llm-probe.json" -w '%{http_code}' --max-time 60 -H "x-goog-api-key: $key" -H 'content-type: application/json' \
        -d '{"contents":[{"parts":[{"text":"reply with ok"}]}]}' \
        "https://generativelanguage.googleapis.com/v1beta/models/${model}:generateContent" || echo 000)
      if [ "$code" != 200 ]; then
        log "WARNING: Gemini ($model) answered HTTP $code: $(grep -o '"message": *"[^"]*' "$STATE/llm-probe.json" | head -1 | cut -c1-200)"
        log "WARNING: the real LLM is not usable: falling back to SOCKABUS_LLM_PROVIDER=fake (canned, offline replies)"
        provider=fake
      fi
      rm -f "$STATE/llm-probe.json"
    fi
  fi
  echo "$provider" > "$STATE/llm-provider"

  if ! running "$STATE/api.pid"; then
    log "building the Go API"
    (cd "$ROOT" && go build -o "$STATE/sockabus" ./cmd/sockabus)
    log "starting the Go API on :$API_PORT (LLM provider: $provider)"
    # env -i: nothing from the caller's shell (or .env) leaks in, DATABASE_URL included.
    cd "$ROOT"
    env -i PATH="$PATH" HOME="$HOME" \
      SOCKABUS_ENV=local SOCKABUS_MODE=live SOCKABUS_SIM=1 \
      SOCKABUS_HTTP_ADDR=":${API_PORT}" \
      SOCKABUS_POSTGRES_URL="$SCRATCH_DB" DATABASE_URL="$SCRATCH_DB" \
      SOCKABUS_REDIS_ADDR="localhost:${REDIS_PORT}" \
      SOCKABUS_TEMPORAL_HOSTPORT="localhost:${TEMPORAL_PORT}" SOCKABUS_TEMPORAL_NAMESPACE=default \
      SOCKABUS_TEMPORAL_TASK_QUEUE=sockabus-e2e \
      SOCKABUS_OPERATOR_TOKEN="$SOCKABUS_OPERATOR_TOKEN" SOCKABUS_OPERATOR_ID=1 \
      SOCKABUS_MESSAGE_KEY="$SOCKABUS_MESSAGE_KEY" \
      SOCKABUS_LLM_PROVIDER="$provider" \
      GEMINI_API_KEY="$(dotenv GEMINI_API_KEY)" ANTHROPIC_API_KEY="$(dotenv ANTHROPIC_API_KEY)" \
      SOCKABUS_PERSONA_MODEL="$(dotenv SOCKABUS_PERSONA_MODEL)" SOCKABUS_EXTRACT_MODEL="$(dotenv SOCKABUS_EXTRACT_MODEL)" \
      SOCKABUS_TG_APP_ID="$(dotenv SOCKABUS_TG_APP_ID)" SOCKABUS_TG_APP_HASH="$(dotenv SOCKABUS_TG_APP_HASH)" \
      SOCKABUS_TG_SESSION_DIR="$STATE/telegram" SOCKABUS_PERSONA_GEN_TIMEOUT="$(dotenv SOCKABUS_PERSONA_GEN_TIMEOUT)" \
      SOCKABUS_REPLY_DELAY_MIN=1s SOCKABUS_REPLY_READ_MIN=1s SOCKABUS_REPLY_READ_MAX=2s SOCKABUS_TYPING_CPS=200 \
      "$STATE/sockabus" < /dev/null > "$STATE/api.log" 2>&1 &
    echo $! > "$STATE/api.pid"
  fi
  wait_for 90 "Go API /readyz" curl -fsS "http://localhost:${API_PORT}/readyz"

  if ! running "$STATE/web.pid"; then
    log "starting the console on :$WEB_PORT"
    cd "$ROOT/web"
    env -i PATH="$PATH" HOME="$HOME" NEXT_TELEMETRY_DISABLED=1 \
      SOCKABUS_API_URL="http://localhost:${API_PORT}" \
      SOCKABUS_OPERATOR_TOKEN="$SOCKABUS_OPERATOR_TOKEN" \
      SOCKABUS_CONSOLE_USER="$SOCKABUS_CONSOLE_USER" SOCKABUS_CONSOLE_PASSWORD="$SOCKABUS_CONSOLE_PASSWORD" \
      NEXT_PUBLIC_UNDO_MS=0 \
      pnpm exec next dev --port "$WEB_PORT" < /dev/null > "$STATE/web.log" 2>&1 &
    echo $! > "$STATE/web.pid"
    cd "$ROOT"
  fi
  wait_for 120 "console /sign-in" curl -fsS -o /dev/null "http://localhost:${WEB_PORT}/sign-in"
  # Compile the pages once (signed in), so the first test doesn't pay for it.
  local jar="$STATE/warmup.cookies"
  curl -s -o /dev/null -c "$jar" --data-urlencode "user=$SOCKABUS_CONSOLE_USER" --data-urlencode "password=$SOCKABUS_CONSOLE_PASSWORD" \
    "http://localhost:${WEB_PORT}/api/auth/sign-in" || true
  for p in / /demo/chat /demo/bank /conversations /personas /verify /status /api/internal/status; do
    curl -s -o /dev/null -b "$jar" --max-time 180 "http://localhost:${WEB_PORT}${p}" || true
  done
  rm -f "$jar"
  log "ready: API http://localhost:${API_PORT}  console http://localhost:${WEB_PORT}  (state in $STATE)"
}

# kill_tree <pid>: the process and all its descendants (pnpm → next → workers).
kill_tree() {
  local c
  for c in $(pgrep -P "$1" 2>/dev/null); do kill_tree "$c"; done
  kill -TERM "$1" 2>/dev/null || true
}

stop_pid() {
  local f=$1
  if running "$f"; then
    local pid; pid=$(cat "$f")
    kill_tree "$pid"
    for _ in $(seq 1 15); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
    kill -KILL "$pid" 2>/dev/null || true
  fi
  rm -f "$f"
}

down() {
  stop_pid "$STATE/web.pid"
  stop_pid "$STATE/api.pid"
  # next dev may leave a worker on the port
  local stray; stray=$(lsof -tiTCP:"$WEB_PORT" -sTCP:LISTEN 2>/dev/null || true)
  [ -n "$stray" ] && kill $stray 2>/dev/null || true
  for s in pg redis temporal; do docker rm -f "$NAME-$s" >/dev/null 2>&1 || true; done
  rm -f "$STATE/stack.env" "$STATE/sockabus" "$STATE/llm-provider"
  log "stopped (logs kept in $STATE/*.log)"
}

status() {
  docker ps -a -f label=sockabus-e2e=1 --format '{{.Names}}\t{{.Status}}\t{{.Ports}}'
  running "$STATE/api.pid" && echo "api  pid $(cat "$STATE/api.pid") :$API_PORT" || echo "api  not running"
  running "$STATE/web.pid" && echo "web  pid $(cat "$STATE/web.pid") :$WEB_PORT" || echo "web  not running"
  [ -f "$STATE/llm-provider" ] && echo "llm  $(cat "$STATE/llm-provider")"
  return 0
}

envout() {
  [ -f "$STATE/stack.env" ] || die "stack is not up (playwright/scripts/e2e-stack.sh up)"
  grep -v '^SOCKABUS_MESSAGE_KEY=' "$STATE/stack.env" | sed 's/^/export /'
  echo "export SOCKABUS_E2E_LLM=$(cat "$STATE/llm-provider" 2>/dev/null || echo unknown)"
  # Direct SQL on the scratch DB for test setup that no API can do (playwright/helpers.ts psql()).
  echo "export SOCKABUS_E2E_PSQL='docker exec -i $NAME-pg psql -U buru -d $DB_NAME'"
}

runtests() {
  guard_db
  [ -f "$STATE/stack.env" ] || die "stack is not up (playwright/scripts/e2e-stack.sh up)"
  eval "$(envout)"
  curl -fsS "$SOCKABUS_API_URL/readyz" >/dev/null || die "API $SOCKABUS_API_URL is not ready"
  cd "$PW" && unset DATABASE_URL SOCKABUS_POSTGRES_URL && exec pnpm exec playwright test "$@"
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  env) envout ;;
  test) shift; runtests "$@" ;;
  *) echo "usage: $0 up|test|down|status|env" >&2; exit 2 ;;
esac
