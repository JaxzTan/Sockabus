#!/usr/bin/env bash
# Schedules playwright/scripts/e2e-monitor.sh (playwright/README.md "Monitoring"). Used by the Makefile:
#
#   playwright/scripts/e2e-monitor-schedule.sh install [minutes] [cron|launchd]   default: 60, cron
#   playwright/scripts/e2e-monitor-schedule.sh uninstall                          removes both mechanisms
#   playwright/scripts/e2e-monitor-schedule.sh status                             schedule + last run summary
#
# cron: one crontab line tagged "# sockabus-e2e-monitor"; every other crontab line is kept as is.
# launchd: ~/Library/LaunchAgents/com.sockabus.e2e-monitor.plist (StartInterval) running the small
# cmd/e2e-launcher binary from ~/Library/Application Support/Sockabus. macOS privacy protection (TCC)
# closes the repo in iCloud Drive to background jobs ("Operation not permitted"): grant that binary Full
# Disk Access once (System Settings > Privacy & Security > Full Disk Access > +).
# Output of the scheduled job (incl. such errors) goes to ~/Library/Logs/sockabus-e2e-monitor.log, outside
# the repo, so a blocked run still leaves a trace. The run's own log is playwright/results/monitor.log.
set -euo pipefail

PW=$(cd "$(dirname "$0")/.." && pwd)   # playwright/
ROOT=$(cd "$PW/.." && pwd)            # repo root
TAG="# sockabus-e2e-monitor"
LABEL=com.sockabus.e2e-monitor
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
OUT="$HOME/Library/Logs/sockabus-e2e-monitor.log"
SCRIPT="$PW/scripts/e2e-monitor.sh"
# launchd runs this binary (cmd/e2e-launcher), not /bin/bash: Full Disk Access is granted to it alone.
LAUNCHER="$HOME/Library/Application Support/Sockabus/e2e-launcher"

# PATH for the job: the directories of the tools the stack needs, as found now, then the system ones.
job_path() {
  local p="" d t
  for t in node pnpm go docker migrate jq curl openssl lsof osascript; do
    d=$(dirname "$(command -v "$t" 2>/dev/null || echo /usr/bin/x)")
    case ":$p:" in *":$d:"*) ;; *) p="${p:+$p:}$d" ;; esac
  done
  for d in /usr/local/bin /opt/homebrew/bin /usr/bin /bin /usr/sbin /sbin; do
    case ":$p:" in *":$d:"*) ;; *) p="$p:$d" ;; esac
  done
  echo "$p"
}

missing() {
  local t m=""
  for t in go pnpm docker migrate jq; do command -v "$t" >/dev/null || m="$m $t"; done
  [ -z "$m" ] || { echo "e2e-monitor-schedule: not on PATH:$m" >&2; exit 1; }
}

# Single-quote for sh: ' -> '\''
sq() { printf "'%s'" "${1//\'/\'\\\'\'}"; }

cron_spec() {
  local m=$1
  if [ "$m" -lt 60 ]; then echo "*/$m * * * *"
  elif [ $((m % 60)) = 0 ] && [ "$m" -lt 1440 ]; then [ "$m" = 60 ] && echo "0 * * * *" || echo "0 */$((m / 60)) * * *"
  else echo "e2e-monitor-schedule: cron needs minutes < 60 or a whole number of hours < 24 (got $m); use launchd" >&2; exit 1
  fi
}

# Writing the crontab can block on a macOS approval prompt ("would like to administer your computer")
# when there is no one to click it: give up after 20 s instead of hanging.
write_crontab() { # write_crontab <file>
  crontab "$1" & local pid=$!
  for _ in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || { wait "$pid"; return $?; }; sleep 1; done
  kill "$pid" 2>/dev/null || true
  echo "e2e-monitor-schedule: crontab is waiting for macOS approval (Privacy & Security). Approve it and retry, or use MECH=launchd." >&2
  return 1
}

uninstall_cron() {
  local cur rest tmp
  cur=$(crontab -l 2>/dev/null) || return 0
  printf '%s\n' "$cur" | grep -qF "$TAG" || return 0 # nothing of ours: leave the crontab alone
  rest=$(printf '%s\n' "$cur" | grep -vF "$TAG" || true)
  tmp=$(mktemp); printf '%s\n' "$rest" > "$tmp"
  write_crontab "$tmp"; rm -f "$tmp"
}

uninstall_launchd() {
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  rm -f "$PLIST"
}

install_cron() {
  local m=$1 spec line
  spec=$(cron_spec "$m")
  line="$spec PATH=$(sq "$(job_path)") /bin/bash $(sq "$SCRIPT") >> $(sq "$OUT") 2>&1 $TAG"
  local tmp; tmp=$(mktemp)
  { crontab -l 2>/dev/null | grep -vF "$TAG" || true; echo "$line"; } > "$tmp"
  write_crontab "$tmp" || { rm -f "$tmp"; exit 1; }
  rm -f "$tmp"
  echo "installed crontab line:"
  echo "  $line"
}

xml() { local s=${1//&/&amp;}; s=${s//</&lt;}; echo "${s//>/&gt;}"; }

# Rebuild only when the binary would change: a new binary loses its Full Disk Access grant.
build_launcher() {
  local tmp; tmp=$(mktemp)
  (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -o "$tmp" ./cmd/e2e-launcher)
  mkdir -p "$(dirname "$LAUNCHER")"
  if [ -f "$LAUNCHER" ] && cmp -s "$tmp" "$LAUNCHER"; then rm -f "$tmp"; else mv "$tmp" "$LAUNCHER"; chmod 755 "$LAUNCHER"; echo "built $LAUNCHER"; fi
}

install_launchd() {
  local m=$1
  build_launcher
  mkdir -p "$(dirname "$PLIST")"
  cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array><string>$(xml "$LAUNCHER")</string><string>$(xml "$SCRIPT")</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>$(xml "$(job_path)")</string></dict>
  <key>StartInterval</key><integer>$((m * 60))</integer>
  <key>RunAtLoad</key><false/>
  <key>StandardOutPath</key><string>$(xml "$OUT")</string>
  <key>StandardErrorPath</key><string>$(xml "$OUT")</string>
  <key>ProcessType</key><string>Background</string>
</dict>
</plist>
EOF
  plutil -lint "$PLIST" >/dev/null
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST"
  echo "installed LaunchAgent $PLIST (every $m min), running $LAUNCHER"
  echo "if runs log 'Operation not permitted' in $OUT: System Settings > Privacy & Security > Full Disk Access > + > $LAUNCHER"
}

status() {
  echo "schedule:"
  local any=0
  if crontab -l 2>/dev/null | grep -F "$TAG"; then any=1; fi
  if [ -f "$PLIST" ]; then
    any=1
    local iv; iv=$(/usr/libexec/PlistBuddy -c 'Print :StartInterval' "$PLIST" 2>/dev/null || echo '?')
    echo "  launchd $LABEL every $((iv / 60)) min ($PLIST)"
    launchctl print "gui/$(id -u)/$LABEL" 2>/dev/null | grep -E '^\s*(state|runs|last exit code)' | sed 's/^[[:space:]]*/    /' || echo "    (not loaded)"
  fi
  [ "$any" = 1 ] || echo "  none (make monitor-install)"
  echo "last monitor run:"
  local last; last=$(ls -1 "$PW"/results/history/*.json 2>/dev/null | sort | tail -n1 || true)
  if [ -n "$last" ]; then
    jq -r --arg f "$(basename "$last")" '"  \($f): started \(.stats.startTime), passed \(.stats.expected), failed \(.stats.unexpected), skipped \(.stats.skipped), flaky \(.stats.flaky), llm \(.config.metadata.llm // "?")"' "$last"
    echo "  runs kept: $(ls -1 "$PW"/results/history/*.json | wc -l | tr -d ' ')"
  else
    echo "  none yet"
  fi
  if [ -f "$PW/results/monitor.log" ]; then
    echo "monitor.log (last 3 lines):"
    tail -n 3 "$PW/results/monitor.log" | sed 's/^/  /'
  fi
  if [ -f "$OUT" ]; then
    echo "scheduler output $OUT (last 3 lines):"
    tail -n 3 "$OUT" | sed 's/^/  /'
  fi
}

case "${1:-}" in
  install)
    missing
    m=${2:-60}; mech=${3:-cron}
    case "$m" in '' | *[!0-9]* | 0) echo "interval must be a positive number of minutes" >&2; exit 2 ;; esac
    uninstall_cron; uninstall_launchd # one mechanism at a time
    case "$mech" in cron) install_cron "$m" ;; launchd) install_launchd "$m" ;; *) echo "mechanism: cron or launchd" >&2; exit 2 ;; esac
    ;;
  uninstall) uninstall_cron; uninstall_launchd; echo "removed the e2e monitor schedule (cron line and LaunchAgent)" ;;
  status) status ;;
  *) echo "usage: $0 install [minutes] [cron|launchd] | uninstall | status" >&2; exit 2 ;;
esac
