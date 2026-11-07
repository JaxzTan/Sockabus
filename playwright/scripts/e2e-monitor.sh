#!/usr/bin/env bash
# Scheduled Playwright run (playwright/README.md "Monitoring"). Run by `make monitor` and by the schedule
# `make monitor-install` sets up (crontab or a launchd LaunchAgent).
#
#   1. takes a lock (playwright/.e2e/monitor.lock), so two runs never overlap
#   2. brings the throwaway test stack up (playwright/scripts/e2e-stack.sh) unless it is already up
#   3. runs the full suite with E2E_LLM_PROVIDER=fake (override: E2E_LLM_PROVIDER=gemini make monitor)
#   4. copies playwright/results/latest.json to playwright/results/history/<UTC timestamp>.json (+ its screenshots
#      in <timestamp>.files/), keeps the last 50
#   5. alerts when the run failed or passed fewer tests than the previous one: macOS notification, plus
#      Telegram (SOCKABUS_ALERT_TELEGRAM_BOT_TOKEN + SOCKABUS_ALERT_TELEGRAM_CHAT_ID) and Uptime Kuma
#      (SOCKABUS_KUMA_E2E_PUSH_URL, pushed every run: up or down) when those are set in .env
#   6. brings the stack down again if this script brought it up
# Everything goes to playwright/results/monitor.log. Exit code: the suite's (0 = all passed), or the stack's.
# Never touches the Compose app/web/postgres/redis/temporal containers or the database in .env.
set -uo pipefail

PW=$(cd "$(dirname "$0")/.." && pwd)   # playwright/
ROOT=$(cd "$PW/.." && pwd)            # repo root
RESULTS="$PW/results"
HIST="$RESULTS/history"
LOG="$RESULTS/monitor.log"
STATE="$PW/.e2e"
LOCK="$STATE/monitor.lock"
KEEP=${E2E_MONITOR_KEEP:-50}
STACK="$PW/scripts/e2e-stack.sh"
export E2E_LLM_PROVIDER=${E2E_LLM_PROVIDER:-fake}
# The suite must never see the production DB (e2e-stack.sh refuses it too).
unset DATABASE_URL SOCKABUS_POSTGRES_URL

mkdir -p "$HIST" "$STATE"

ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
log() { echo "[$(ts)] monitor: $*" >> "$LOG"; }

# Only these keys come from .env (it also holds the production DATABASE_URL, never sourced).
dotenv() {
  [ -f "$ROOT/.env" ] || return 0
  grep -E "^$1=" "$ROOT/.env" | tail -n1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//"
}

# ---- lock: mkdir is atomic; a lock whose pid is gone is stale ----
if ! mkdir "$LOCK" 2>/dev/null; then
  other=$(cat "$LOCK/pid" 2>/dev/null || true)
  if [ -n "$other" ] && kill -0 "$other" 2>/dev/null; then
    log "another run (pid $other) is still going: skipping this one"
    echo "e2e-monitor: another run (pid $other) holds $LOCK: skipped" >&2
    exit 75 # EX_TEMPFAIL
  fi
  log "removing stale lock (pid ${other:-?} is gone)"
  rm -rf "$LOCK"
  mkdir "$LOCK" || { log "could not take the lock"; exit 75; }
fi
echo $$ > "$LOCK/pid"

started=0
cleanup() {
  if [ "$started" = 1 ]; then
    log "bringing the test stack down (this run brought it up)"
    "$STACK" down >> "$LOG" 2>&1 || log "WARNING: e2e-stack.sh down failed"
  fi
  rm -rf "$LOCK"
}
trap cleanup EXIT
trap 'log "interrupted"; exit 130' INT TERM

# Keep the log to the last 5000 lines.
if [ -f "$LOG" ] && [ "$(wc -l < "$LOG")" -gt 5000 ]; then
  tail -n 4000 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
fi

stamp=$(date -u +%Y%m%dT%H%M%SZ)
log "=== run $stamp (LLM provider requested: $E2E_LLM_PROVIDER, pid $$)"

notify() { # notify <title> <message>
  local title=$1 msg=$2
  if command -v osascript >/dev/null 2>&1; then
    # Text as arguments, so quotes in it can't break the AppleScript.
    osascript -e 'on run argv' -e 'display notification (item 2 of argv) with title (item 1 of argv)' -e 'end run' "$title" "$msg" >/dev/null 2>&1 &&
      log "macOS notification shown" || log "WARNING: macOS notification failed"
  fi
  local tok chat
  tok=$(dotenv SOCKABUS_ALERT_TELEGRAM_BOT_TOKEN)
  chat=$(dotenv SOCKABUS_ALERT_TELEGRAM_CHAT_ID)
  if [ -n "$tok" ] && [ -n "$chat" ]; then
    # The token goes in the URL, so curl reads it from a config on stdin: never in ps or the log.
    if printf 'url = "https://api.telegram.org/bot%s/sendMessage"\n' "$tok" |
      curl -fsS -o /dev/null --max-time 20 -K - --data-urlencode "chat_id=$chat" --data-urlencode "text=$title: $msg"; then
      log "Telegram alert sent"
    else
      log "WARNING: Telegram alert failed"
    fi
  fi
}

kuma() { # kuma <up|down> <message>
  local url; url=$(dotenv SOCKABUS_KUMA_E2E_PUSH_URL)
  [ -n "$url" ] || return 0
  url=${url%%\?*} # the push URL's own ?status=up&msg=OK&ping= is replaced
  if printf 'url = "%s"\n' "$url" | curl -fsS -o /dev/null --max-time 20 -G -K - --data-urlencode "status=$1" --data-urlencode "msg=$2"; then
    log "Uptime Kuma push sent ($1)"
  else
    log "WARNING: Uptime Kuma push failed"
  fi
}

# Counts from a Playwright JSON report: "passed failed skipped flaky".
counts() { jq -r '.stats | "\(.expected) \(.unexpected) \(.skipped) \(.flaky)"' "$1" 2>/dev/null; }

# ---- 1. stack ----
stack_up() {
  [ -f "$STATE/stack.env" ] || return 1
  # shellcheck disable=SC1091
  ( . "$STATE/stack.env" && curl -fsS -o /dev/null --max-time 5 "$SOCKABUS_API_URL/readyz" && curl -fsS -o /dev/null --max-time 10 "$SOCKABUS_WEB_URL/sign-in" )
}

if stack_up; then
  log "test stack already up: using it as is ($(cat "$STATE/llm-provider" 2>/dev/null || echo '?') LLM), leaving it up afterwards"
else
  log "starting the test stack"
  started=1
  if ! "$STACK" up >> "$LOG" 2>&1; then
    log "FAILED: the test stack did not come up (see above)"
    notify "Sockabus e2e monitor" "Run $stamp: the test stack did not start. See playwright/results/monitor.log"
    kuma down "stack did not start"
    exit 1
  fi
fi

# ---- 2. suite ----
marker="$STATE/monitor.marker"
: > "$marker"
log "running the Playwright suite"
"$STACK" test >> "$LOG" 2>&1
rc=$?
log "playwright exited $rc"

latest="$RESULTS/latest.json"
if [ ! -f "$latest" ] || [ ! "$latest" -nt "$marker" ]; then
  log "FAILED: playwright wrote no playwright/results/latest.json"
  notify "Sockabus e2e monitor" "Run $stamp: Playwright produced no results (exit $rc). See playwright/results/monitor.log"
  kuma down "no results (exit $rc)"
  [ "$rc" = 0 ] && rc=1
  exit "$rc"
fi

# ---- 3. history ----
prev=$(ls -1 "$HIST"/*.json 2>/dev/null | sort | tail -n1)
cp "$latest" "$HIST/$stamp.json"
# Screenshots of this run (test-results/ is overwritten by the next run).
if [ -d "$PW/test-results" ]; then
  (cd "$PW/test-results" && find . -name '*.png' -print0 | while IFS= read -r -d '' f; do
    mkdir -p "$HIST/$stamp.files/$(dirname "$f")" && cp "$f" "$HIST/$stamp.files/$f"
  done)
fi
# Keep the newest $KEEP runs.
total=$(ls -1 "$HIST"/*.json 2>/dev/null | wc -l | tr -d ' ')
[ "$total" -gt "$KEEP" ] && ls -1 "$HIST"/*.json | sort | head -n "$((total - KEEP))" | while IFS= read -r old; do
  rm -rf "$old" "${old%.json}.files"
  log "pruned $(basename "$old")"
done

read -r passed failed skipped flaky <<< "$(counts "$latest")"
summary="passed $passed, failed $failed, skipped $skipped, flaky $flaky ($(jq -r '.config.metadata.llm // "unknown"' "$latest") LLM)"
log "result: $summary -> history/$stamp.json"

# ---- 4. alerts ----
drop=""
if [ -n "$prev" ]; then
  read -r ppassed _ <<< "$(counts "$prev")"
  if [ -n "${ppassed:-}" ] && [ "${passed:-0}" -lt "$ppassed" ]; then drop="pass count dropped $ppassed -> $passed"; fi
fi
if [ "$rc" != 0 ] || [ -n "$drop" ]; then
  why=${drop:-"$failed failed"}
  [ -n "$drop" ] && [ "$rc" != 0 ] && why="$failed failed; $drop"
  log "ALERT: $why"
  notify "Sockabus e2e: $why" "Run $stamp: $summary"
  kuma down "$why"
else
  kuma up "$summary"
fi
log "=== done $stamp (exit $rc)"
exit "$rc"
