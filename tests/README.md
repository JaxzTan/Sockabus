# Playwright tests (TRD §9)

| Spec | What it covers |
|---|---|
| `tests/api/endpoints.spec.ts` | One test per HTTP route: `/healthz`, `/readyz` (server.go), every route in `internal/server/live.go` (incl. `/internal/sim/*`, `/internal/status`, `/v1/lookup`) and the console routes in `web/src/app/api/**/route.ts`. Status codes, 401 without/with a wrong token or session, response shape, 400/404 paths. |
| `tests/api/journeys.spec.ts` | a. persona generation · b. Telegram accounts/connect/chat · c. scam flow (web chat → replies → stages rapport/bait/payment_request/stall + stage history → takeover → extraction → verify → bank hit) · d. AI summary: conversation, persona and dashboard overview endpoints, then the dashboard card, persona detail and conversation view |
| `tests/api/demo-path.spec.ts` | v1.0 demo path through the API, one step per stage |
| `tests/ui/console.spec.ts` | e. bank simulator (`/demo/bank`): held hit and clean miss · f. web chat (`/demo/chat`) + conversations page: messages, stage bar, polling without reload, operator takeover |
| `tests/ui/webchat.spec.ts` | web chat regression: reply + captured account |
| `tests/ui/undecryptable.spec.ts` | g. a real conversation with one stored message corrupted on the test DB (`SOCKABUS_E2E_PSQL`): the API flags it `undecryptable`, the conversations page shows a muted placeholder bubble, the persona keeps replying and the AI summary still works |

Rules the suite keeps: no mocks or `page.route`, no stubbed responses, no hardcoded IDs; every input
(persona, scammer name, messages, account numbers) is generated per run; no `test.skip` / `fixme` / `only`.
A missing or broken feature fails its test with a message saying what is missing.

## Run it locally (throwaway stack, never Supabase)

The suite writes append-only rows (messages, sightings, verifications), so it never runs against the
Compose `app`/`web` containers or the database in `.env`. `scripts/e2e-stack.sh` starts its own stack:

| Piece | Where |
|---|---|
| PostgreSQL 16 (`sockabus_e2e`, migrated + seeded) | container `sockabus-e2e-pg`, `localhost:15432` |
| Redis | container `sockabus-e2e-redis`, `localhost:16379` |
| Temporal dev server | container `sockabus-e2e-temporal`, `localhost:17233` (own server, task queue `sockabus-e2e`) |
| Go API (`go build ./cmd/sockabus`, live mode, `SOCKABUS_SIM=1`) | `http://localhost:18080` |
| Console (`next dev`, sign-in on with generated credentials) | `http://localhost:13000` |

```sh
pnpm install && pnpm exec playwright install chromium   # once
scripts/e2e-stack.sh up        # containers, migrate + seed, API, console; waits until ready
scripts/e2e-stack.sh test      # full suite; extra args go to playwright (e.g. tests/ui, -g "bank")
scripts/e2e-stack.sh down      # stop everything and delete the scratch DB
scripts/e2e-stack.sh status    # what is running, and which LLM the API uses
eval "$(scripts/e2e-stack.sh env)"   # the test variables, to run `pnpm exec playwright ...` yourself
```

`up` and `test` refuse to run when `DATABASE_URL` or `SOCKABUS_POSTGRES_URL` contains `supabase`, and the
API is started with `env -i`, so nothing from your shell or `.env` (the production `DATABASE_URL`) reaches it.
From `.env` it reads only the LLM keys/models and the Telegram app id/hash. The Telegram session dir is
`.e2e/telegram` (empty), so the production sessions in `.secrets/telegram` are never opened.

**LLM.** The API uses the provider and models in `.env` (Claude: `SOCKABUS_LLM_PROVIDER=anthropic`,
persona `claude-sonnet-5-5`, extract/summaries `claude-haiku-5-5`) so replies, generated personas and AI
summaries are real model output. `up` first sends one tiny Claude request; if the key is rejected or out
of credit it prints a warning and falls back to `SOCKABUS_LLM_PROVIDER=fake` (offline canned replies and
summaries). Force it with `E2E_LLM_PROVIDER=fake scripts/e2e-stack.sh up`. The provider in use is in
`scripts/e2e-stack.sh status` and in the HTML report's metadata (`llm`). Note: the stack shares the
Anthropic key with production, so a run spends production's credit (a full run is a few dozen calls).

**Telegram (journey b).** It needs a real, logged-in honeypot account in the scratch DB. Without one the
test fails with "No Telegram session exists in the test stack". To run it, log a *test* number in against
the e2e stack (never a production number already used by the `app` container):
`SOCKABUS_POSTGRES_URL=postgres://buru:buru@localhost:15432/sockabus_e2e?sslmode=disable SOCKABUS_TG_SESSION_DIR=.e2e/telegram go run ./cmd/tglogin -phone +60...`,
then message it from another Telegram account.

## Results

- `playwright-report/index.html`: HTML report (`pnpm exec playwright show-report`), with steps, traces and,
  for UI tests, a screenshot and trace of every test.
- `tests/results/latest.json`: JSON results of the last run.
- `test-results/`: per-test traces (`pnpm exec playwright show-trace <path>/trace.zip`) and screenshots.
- `.e2e/api.log`, `.e2e/web.log`: backend and console logs of the test stack.

## Monitoring

`scripts/e2e-monitor.sh` runs the whole suite on the throwaway stack and keeps a history. It brings the
stack up if it is not already up (and down again afterwards), uses `E2E_LLM_PROVIDER=fake` unless you
set another, copies `tests/results/latest.json` to `tests/results/history/<UTC timestamp>.json` with that
run's screenshots in `<timestamp>.files/` (last 50 runs kept), logs to `tests/results/monitor.log` and
exits with the suite's exit code. A lock (`.e2e/monitor.lock`) makes an overlapping run skip (exit 75).

```sh
make monitor                              # one run now
make monitor-install                      # every 60 min with cron
make monitor-install INTERVAL=30 MECH=launchd   # launchd LaunchAgent instead (see below)
make monitor-status                       # installed schedule + last run summary
make monitor-uninstall                    # removes the cron line and the LaunchAgent; other crontab lines stay
```

**Alerts** fire when the run fails or passes fewer tests than the previous one: a macOS notification,
plus Telegram when `.env` has `SOCKABUS_ALERT_TELEGRAM_BOT_TOKEN` and `SOCKABUS_ALERT_TELEGRAM_CHAT_ID`,
and an Uptime Kuma push (`status=up` every good run, `status=down` on an alert) when it has
`SOCKABUS_KUMA_E2E_PUSH_URL` (a Kuma "Push" monitor; set its heartbeat interval a bit above the schedule).
Only these keys are read from `.env`. While the suite has known failures, every run alerts.

**cron vs launchd.** The repo is in iCloud Drive, which macOS privacy protection (TCC) closes to
background jobs. On this Mac, writing the crontab from a non-interactive shell blocked on an "administer
your computer" approval, and a LaunchAgent running `/bin/bash` directly was refused ("Operation not
permitted", exit 126). So use `MECH=launchd` (`make monitor-install MECH=launchd`): it builds
`cmd/e2e-launcher` into `~/Library/Application Support/Sockabus/e2e-launcher` and installs
`~/Library/LaunchAgents/com.sockabus.e2e-monitor.plist` (`StartInterval`) that runs it. The launcher is the
process TCC checks, so any access grant goes to that one binary, not to `/bin/bash`. If a run is
refused, the launcher shows a notification naming the binary to add under System Settings > Privacy &
Security > Full Disk Access. It is only rebuilt when its code changes, because a rebuilt binary loses its
grant. `make monitor-install` (cron) gives up after 20 s if the crontab write blocks. The scheduler's own
output goes to `~/Library/Logs/sockabus-e2e-monitor.log`. A LaunchAgent runs only while you are logged in,
and a sleeping Mac runs a missed interval once on wake.

**Console.** `/automate` in the console shows the latest run (start time, duration, LLM provider, totals),
every test with its steps and error, the screenshots, and the monitor history. It reads the results
directory on the server: `SOCKABUS_E2E_RESULTS_DIR` (default `<repo>/tests/results` when run from `web/`);
the HTML report (`/api/automate/report/index.html`, or `pnpm exec playwright show-report`) and
`test-results/` are taken from two levels above it. The Compose `web` service mounts these three
directories read-only under `/e2e`.

## Backend settings (any other stack, e.g. CI)

| Variable | Value | Why |
|---|---|---|
| `SOCKABUS_MODE` | `live` | PostgreSQL-backed API |
| `SOCKABUS_SIM` | `1` | `/internal/sim/*` (web chat) |
| `SOCKABUS_LLM_PROVIDER` | `anthropic` (real, default) or `fake` (offline, CI) | persona replies, generation, AI summaries |
| `SOCKABUS_REPLY_DELAY_MIN` | `1s` | FR-3 floor; refused when `SOCKABUS_ENV=prod` |
| `SOCKABUS_REPLY_READ_MIN` / `_MAX` | `1s` / `2s` | first reply within a few seconds |
| `SOCKABUS_TYPING_CPS` | `200` | short "typing…" phase |
| `SOCKABUS_TEMPORAL_TASK_QUEUE` | e.g. `sockabus-e2e` | keeps test workflows off another worker's queue |
| `SOCKABUS_TG_APP_ID` / `_APP_HASH` | any values, with an empty session dir | Telegram connector on, so `/internal/accounts/{id}/connect` answers instead of 503 |
| `SOCKABUS_E2E_PSQL` (test runner) | a psql command reading SQL on stdin, e.g. `docker compose exec -T postgres psql -U buru -d buru` | direct DB setup in `undecryptable.spec.ts` (corrupting one ciphertext); `scripts/e2e-stack.sh env` sets it for the local stack. Never Supabase (the helper refuses it). |

The database must be migrated and seeded (`db/seed/001_demo.sql` creates operator 1 and the playbooks).
The console (`web/`) needs `SOCKABUS_API_URL`, `SOCKABUS_OPERATOR_TOKEN` and console sign-in on
(`SOCKABUS_CONSOLE_USER` / `SOCKABUS_CONSOLE_PASSWORD`); pass the same credentials to the tests, together
with `SOCKABUS_API_URL`, `SOCKABUS_WEB_URL` and `SOCKABUS_OPERATOR_TOKEN`.

CI runs both projects on every pull request (`.github/workflows/ci.yml`, job `e2e`) with the fake LLM.
Journey b (Telegram) fails there until a Telegram session exists; journey d (AI summary) passes with the fake LLM too (canned summary text);
that is intended: the suite reports missing features instead of skipping them.
