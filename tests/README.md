# Playwright tests (TRD §9)

`tests/api/` drives the v1.0 demo path through the Go API: generate persona → scammer message
(web chat simulator) → persona reply → takeover → operator message → extracted bank account →
verify → `POST /v1/lookup` hit. `tests/ui/` runs the same flow through the console's web chat page.

Each run creates its own persona, scammer and account number, then closes the conversation and
retires the persona, so reruns pass on the same database. Messages, sightings and verifications are
append-only, so test rows stay behind; point the suite at a scratch database, never Supabase.

## Backend settings

| Variable | Value | Why |
|---|---|---|
| `SOCKABUS_MODE` | `live` | PostgreSQL-backed API |
| `SOCKABUS_SIM` | `1` | `/internal/sim/*` (web chat) |
| `SOCKABUS_LLM_PROVIDER` | `fake` | offline, deterministic replies |
| `SOCKABUS_REPLY_DELAY_MIN` | `1s` | FR-3 floor; refused when `SOCKABUS_ENV=prod` |
| `SOCKABUS_REPLY_READ_MIN` / `_MAX` | `1s` / `2s` | first reply within a few seconds |
| `SOCKABUS_TYPING_CPS` | `200` | short "typing…" phase |
| `SOCKABUS_TEMPORAL_TASK_QUEUE` | e.g. `sockabus-e2e` | keeps test workflows off another worker's queue |

The database must be migrated and seeded (`db/seed/001_demo.sql` creates operator 1 and the playbooks).
The console (`web/`) needs `SOCKABUS_API_URL` and `SOCKABUS_OPERATOR_TOKEN` for that backend; with
console sign-in on, also pass `SOCKABUS_CONSOLE_USER` / `SOCKABUS_CONSOLE_PASSWORD` to the tests.

## Run

```sh
pnpm install && pnpm exec playwright install chromium
SOCKABUS_API_URL=http://localhost:8080 SOCKABUS_WEB_URL=http://localhost:3000 \
SOCKABUS_OPERATOR_TOKEN=<backend token> pnpm test:e2e     # or test:api / test:ui
```

CI runs both projects on every pull request (`.github/workflows/ci.yml`, job `e2e`).
