# Explanation: v1.0 readiness changes (31 Oct 2026)

What changed on `feature/FS_V1` after commit `2fc2e85`, why, and how to use it. The changes close the
backend gaps in [TRD.md](TRD.md) §13 (status page, monitoring, automated tests, Supabase) for the
2 Nov bank demo. Progress against the PRD and TRD is tracked in
[progress_accoring_prdtrd.md](progress_accoring_prdtrd.md).

## Summary

| Area | Change | Requirement |
|---|---|---|
| Status | `GET /internal/status`; Temporal added to `/readyz`; status page reads both | NFR-9, TR-20 |
| Telegram | Connected flag, last-message time and an Uptime Kuma heartbeat in the connector | NFR-9, TR-17, TR-20 |
| LLM | `fake` provider for CI; failed-call counter for the status page | NFR-10, TR-21 |
| Reply timing | The 10 s FR-3 floor is configurable for CI; prod refuses lower values | FR-3, TR-3 |
| Database | Every DB script and `make` target targets Supabase; the prod stack has no local Postgres | NFR-11, TR-23 |
| Tests | Playwright API and UI tests for the demo path; new CI job `e2e` | NFR-10, TR-21 |
| Docs | Architecture diagram, `docs/makefile.md`, `docs/docker.md`, `playwright/README.md` | — |

No API path was renamed. `git diff` shows 22 changed files; there are about 15 new files.

## 1. Status page and health checks

**`GET /internal/status`** (operator token required; 401 without it):

```json
{
  "telegram": { "connected": true, "last_message_at": "2026-10-31T08:41:00Z" },
  "llm": { "errors_recent": 0 },
  "workflows": { "running": 3 }
}
```

- `telegram.connected` is true while at least one honeypot account holds a live MTProto session.
  `last_message_at` is the last private message received since the process started (`null` if none).
  Messages from the web chat simulator do not count; they don't go through Telegram.
- `llm.errors_recent` counts LLM calls that still failed after retries, over the last 15 minutes.
  Persona replies, extraction and persona generation all count. A caller cancelling is not an error.
- `workflows.running` is the number of running Temporal workflows. If Temporal can't answer, the
  value is `null` and `workflows.error` says `"temporal unavailable"`; the endpoint still returns 200.

**`/readyz`** now checks Temporal next to PostgreSQL and Redis and returns 503 when any of them is
down. **`/healthz`** stays a pure liveness check (always 200), so a database or Temporal outage never
gets the process restarted.

**Status page** (`web/src/app/(console)/status/view.tsx`): the Telegram, last-message, AI model and
conversations tiles read `/internal/status` through the existing server-side proxy
(`web/src/app/api/[...path]/route.ts`), so the token never reaches the browser. The header says
"Degraded: Telegram disconnected" when accounts are bound but none is connected. The "Not reported"
text is gone. If `/internal/status` can't be reached, the tiles fall back to database data.

Code: `internal/server/live.go` (`getStatus`, `StatusSources`), `internal/server/server.go`
(`WithStatus`, `WithTemporalCheck`), `internal/llm/errcount.go`, `cmd/sockabus/main.go` (wiring).

## 2. Telegram heartbeat

While an account is connected, the connector GETs `SOCKABUS_KUMA_TG_PUSH_URL` once at connect and then
every 60 s. It stops as soon as the connection drops, so an Uptime Kuma push monitor alerts within
2 minutes (60 s interval + 1 retry, TR-20). With the variable empty it does nothing. The URL contains
Kuma's push token, so it is never logged.

Code: `internal/channel/telegram/manager.go` (`heartbeat`, `Status`).

## 3. Fake LLM and reply timing (for tests)

**`SOCKABUS_LLM_PROVIDER=fake`** answers without any network call, and the same input always gets the
same answer:

| Prompt | Fake answer |
|---|---|
| Persona reply | One of five fixed Manglish lines, picked by the scammer's latest message |
| Extraction | No extra entities; the regex extractor still finds bank accounts, wallets, phones |
| Classification | `other`, so the keyword classifier decides |
| Persona generator | Valid draft personas with names that don't clash with existing ones |

The existing selector `SOCKABUS_LLM_PROVIDER` was kept (it is also the name in TRD §9) instead of
adding a second `SOCKABUS_LLM` variable.

**`SOCKABUS_REPLY_DELAY_MIN`** (default `10s`) is the FR-3 floor: no reply sooner than this after the
scammer's last message. Tests use `1s`. The app refuses to start with a value below 10 s when
`SOCKABUS_ENV=prod`, and logs a warning in any other environment. Workflows that were already running
keep the 10 s floor.

Code: `internal/llm/fake.go`, `internal/workflows/conversation.go` (`Timing.MinDelay`),
`internal/config/config.go`.

## 4. Database: everything on Supabase

### Two logins

| Variable | Login | Used by |
|---|---|---|
| `DATABASE_URL` | `postgres.<project-ref>` (owner) | `migrate-up/down`, `test-db`, `db-check`, `app-role`, `seed`, `backup`, `make deploy` |
| `SOCKABUS_POSTGRES_URL` | `buru_api.<project-ref>` (member of `buru_app`) | The Go app only |

Both go through the **session pooler** on port 5432
(`aws-0-<region>.pooler.supabase.com`, `?sslmode=require`). The direct host `db.<ref>.supabase.co` is
IPv6-only and Docker can't reach it. The app must not use the owner login: `buru_app` has no
UPDATE or DELETE on `messages`, `entity_sightings` and `verifications`, which is what keeps the audit
trail tamper-proof (NFR-8).

### New and changed targets

| Command | What it does |
|---|---|
| `make db-check` | Read-only. Shows the applied migration version and counts rows that would break migration 000005's constraints. All counts must be 0 before `make migrate-up` |
| `make app-role` | Creates the `buru_api` login (or gives it a new password) and prints the password once |
| `make seed` | Loads `db/seed/001_demo.sql` (operator 1, playbooks, demo personas). Safe to rerun |
| `make backup` | `pg_dump` of the `public` schema into `backups/`, gzipped. Optional rclone copy and Kuma push on success. A failed dump leaves only a `.part` file |
| `make restore-test` | Restores the newest dump into a throwaway PostgreSQL container, counts rows, removes the container. Never touches Supabase |
| `make test-e2e` | Always uses the Playwright scratch DB (`make e2e-up`), because it writes append-only rows that can't be deleted |

All of them run without a local `psql`: `scripts/pg.sh` runs the PostgreSQL 17 client from the
`postgres:17-alpine` image, and passes the URL as an environment variable so it never appears in `ps`.

### Prod stack

There is no local `postgres` container in either compose file. The `migrate` service lives only in
`docker-compose.prod.yml` and uses `DATABASE_URL`. `make deploy` starts only Redis before migrating. Temporal is unaffected:
in prod it stores its history in SQLite on its own volume.

### Migration 000005

`000005_webchat_integrity` adds the `webchat` channel and four integrity constraints. It migrated up,
down and up again cleanly on local PostgreSQL, and `sqlc generate` produced no diff. It has **not**
been applied to Supabase: run `make db-check` first.

## 5. Automated tests

| Layer | Where | Covers |
|---|---|---|
| Go unit | `internal/server/status_test.go`, `internal/llm/errcount_test.go`, `internal/channel/telegram/heartbeat_test.go`, `internal/workflows/conversation_test.go`, `internal/llm/llm_test.go`, `internal/persona/generator_test.go` | Status endpoint and 401, error window, heartbeat start and stop, lowered reply floor, fake determinism, generator with the fake |
| Playwright API | `playwright/api/demo-path.spec.ts` | Generate → activate → scammer message → persona reply → takeover → operator message → bank account extracted → verify → `/v1/lookup` hit and miss → status; plus 401 checks |
| Playwright UI | `playwright/ui/webchat.spec.ts` | Web chat page: send a message, see the persona reply and the captured account |

Each run makes its own persona, scammer and account number, then closes the conversation and retires
the persona, so reruns pass on the same database. Settings and commands are in
[playwright/README.md](../playwright/README.md). Playwright is a self-contained project in `playwright/` (its own `package.json`,
`Makefile`, config and scripts), version 1.64.1.

**CI job `e2e`** (`.github/workflows/ci.yml`): writes a throwaway `.env` (fake LLM, local Postgres,
fresh keys), starts the Compose stack, waits for `/readyz` and the console, migrates and seeds, runs
both Playwright projects, uploads the HTML report and Compose logs, then tears down. It has not run on
GitHub yet.

## 6. New environment variables

| Variable | Default | Purpose |
|---|---|---|
| `SOCKABUS_LLM_PROVIDER=fake` | `gemini` | Offline provider for CI and tests |
| `SOCKABUS_REPLY_DELAY_MIN` | `10s` | FR-3 floor; below 10 s refused in prod |
| `SOCKABUS_KUMA_TG_PUSH_URL` | empty | Telegram heartbeat push URL |
| `SOCKABUS_KUMA_BACKUP_PUSH_URL` | empty | Push after each successful backup |
| `PG_CLIENT_IMAGE` | `postgres:17-alpine` | Image `scripts/pg.sh` and `restore-test` use |

## 7. Known issues found while testing

| Issue | Effect | Status |
|---|---|---|
| One conversation that can't be decrypted (for example, encrypted with another `SOCKABUS_MESSAGE_KEY`) makes `GET /internal/conversations/{id}` fail | Every console page shows "internal error" | Open |
| Local `.env` `DATABASE_URL` uses the IPv6-only direct host | Fails from Docker and most VPSs | Fix in `.env` |
| Local `.env` `SOCKABUS_POSTGRES_URL` logs in as the owner | App can edit audit tables | Fix with `make app-role` |
| Uptime Kuma, Caddy and the VPS are not live | Release criteria 2 and 3 can't be met by 1 Nov | Waiting on the VPS |

## 8. Testing plan

### Before committing

1. `go vet ./... && go test -race ./...`
2. `cd web && pnpm lint && pnpm build`
3. `make e2e-up && make test-db` (throwaway scratch DB)
4. Playwright against a local backend started with the settings in [playwright/README.md](../playwright/README.md):
   `cd playwright && pnpm test:e2e`
5. `docker compose -f docker-compose.yml -f docker-compose.prod.yml --profile app config --quiet`
   (needs `SOCKABUS_DOMAIN` set)

### After pushing

1. All four CI jobs pass: `test`, `db`, `web`, `e2e`. If `e2e` fails, download the
   `playwright-report` artifact (it includes `compose.log`).
2. Turn on branch protection for `main` with those four jobs required.

### Before the demo

1. Supabase: `make db-check` → `make migrate-up` → `make app-role` → `make seed` →
   `make backup && make restore-test`.
2. Run the stack against Supabase (`make all`) with a real LLM key and `SOCKABUS_ENV=prod`, so the
   10 s floor applies.
3. Send a real Telegram message to the honeypot number; confirm the persona replies within 10–45 s and
   the status page says Connected with a recent last-message time.
4. Check the status page: Backend health 200, AI model 0 errors, Conversations running is a number,
   not "Unknown". (Kuma alerting waits for the VPS.)
5. Rehearse the whole demo twice: generate persona → chat (Telegram, then the web chat fallback) →
   takeover → verify the bank account → bank simulator returns a hold → status page.
6. Run `cd playwright && pnpm test:api` against the demo stack once, using a throwaway persona. The test rows stay in
   the database, so do this only if that's acceptable on Supabase.
