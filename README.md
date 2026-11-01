# Sockabus

> Scam intelligence network for Southeast Asia: AI honeypot personas that turn scammer chats into verified intel for banks and e-wallets.

![Language](https://img.shields.io/badge/language-Go-blue)
![License](https://img.shields.io/badge/license-MIT-lightgrey)

## Table of Contents
- [About](#about)
- [Features](#features)
- [Tech Stack](#tech-stack)
- [Getting Started](#getting-started)
- [Usage](#usage)
- [Testing](#testing)
- [Project Structure](#project-structure)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)
- [Team](#team)

## About

Multilingual AI personas run on honeypot messaging accounts and keep scammers talking. Sockabus pulls mule bank accounts, crypto wallets, phone numbers and URLs out of those conversations, and serves verified intelligence to banks, e-wallets and exchanges through a low-latency lookup API.

- Product requirements: [docs/PRD.md](docs/PRD.md)
- Technical design: [docs/TRD.md](docs/TRD.md)
- Live Telegram honeypot setup and testing: [docs/telegram-honeypot.md](docs/telegram-honeypot.md)
- Playwright end-to-end tests: [tests/README.md](tests/README.md)

## Features

**Working today** (in memory, against a scripted scammer simulator):
- Full honeypot loop: message → entity extraction → scam classification → playbook stage → persona reply
- LLM-powered personas with a prompt-injection guard (Gemini in the demo, Claude in production)
- Playbook stage machine: rapport → bait → payment request → stall
- Entity extraction for bank accounts, phone numbers, TRON/ETH wallets and URLs
- Operator takeover and verify/reject; only verified entities return `high` confidence
- `POST /v1/lookup` customer API
- Next.js operator console and customer portal (on mock data)
- Ops status (NFR-9): `GET /internal/status` reports Telegram connection + last message time, LLM errors in the last 15 min and running Temporal workflows; `/readyz` now also checks Temporal; the console `/status` page shows all of it
- Telegram connector heartbeat to an Uptime Kuma push monitor every 60 s (TR-20)
- Offline `fake` LLM provider for CI and tests
- Playwright API + UI tests of the v1.0 demo path, run in CI against the Compose stack (TRD §9)

**Planned:** see [Roadmap](#roadmap).

## Tech Stack

- **Frontend:** Next.js + Tailwind (operator console and customer portal)
- **Backend:** Go modular monolith, Temporal (one workflow per conversation), LLM: Gemini API (demo) → Claude API (production)
- **Database:** PostgreSQL on Supabase (sqlc + pgx), Redis
- **Infra / Deployment:** Supabase (session-mode pooler), Docker / Docker Compose, Caddy, Uptime Kuma
- **Testing:** `go test`, Playwright (API + UI), GitHub Actions

## Getting Started

### Prerequisites
- Go 1.26+
- Node 24 + pnpm
- Docker
- [`migrate`](https://github.com/golang-migrate/migrate)

### Installation
```bash
git clone https://github.com/JaxzTan/Buru_AI.git
cd Buru_AI
cd web && pnpm install && cd ..
```

### Environment Variables
Copy `.env.example` to `.env` at the repo root. Key variables:

| Variable | Description |
|----------|-------------|
| `SOCKABUS_ENV` | Environment name |
| `SOCKABUS_HTTP_ADDR` | API listen address |
| `SOCKABUS_POSTGRES_URL` | PostgreSQL connection string |
| `SOCKABUS_REDIS_ADDR` | Redis address |
| `SOCKABUS_TEMPORAL_HOSTPORT` | Temporal server host:port |
| `SOCKABUS_TEMPORAL_NAMESPACE` | Temporal namespace |
| `SOCKABUS_TEMPORAL_TASK_QUEUE` | Temporal task queue (e.g. `sockabus-main`) |
| `SOCKABUS_LLM_PROVIDER` | `gemini` (default) \| `anthropic` \| `fake` (offline, CI/tests) \| `none` |
| `SOCKABUS_REPLY_DELAY_MIN` | FR-3 reply floor (default `10s`). Lower only in CI/tests; refused when `SOCKABUS_ENV=prod` |
| `SOCKABUS_KUMA_TG_PUSH_URL` | Uptime Kuma push URL for the Telegram heartbeat. Empty = off |
| `SOCKABUS_KUMA_BACKUP_PUSH_URL` | Uptime Kuma push URL hit after each successful backup. Empty = off |
| `SUPABASE_PASSWORD` | Supabase database password |
| `NEXT_PUBLIC_SUPABASE_URL` | Supabase project URL (dashboard) |
| `NEXT_PUBLIC_SUPABASE_PUBLISHABLE_KEY` | Supabase publishable key (dashboard) |
| `DATABASE_URL` | Supabase owner login (session pooler, port 5432). Used by migrations, `seed`, `backup`, `db-check`, `app-role`. Never given to the app |
| `DIRECT_URL` | Direct database connection (migrations) |

### Run
```bash
make up            # PostgreSQL :5432, Redis :6379, Temporal :7233 (UI :8233)
make migrate-up
make run           # API on :8080 (SOCKABUS_MODE=demo: in-memory pitch data)
curl localhost:8080/readyz
make web           # dashboard on :3000
```

Live honeypot on a real Telegram account (`SOCKABUS_MODE=live`): see [docs/telegram-honeypot.md](docs/telegram-honeypot.md).

```sh
make seed && make tglogin PHONE=+60XXXXXXXXX PERSONA=1 && make run
```

Smoke-test the Temporal worker:

```bash
docker compose exec temporal temporal workflow execute \
  --type Ping --task-queue sockabus-main --input '"sockabus"'
```

## Usage

| Command | Does |
|---|---|
| `make up` / `make down` | Start / stop local dependencies |
| `make migrate-up` / `make migrate-down` | Apply / roll back one migration |
| `make sqlc` | Generate `internal/db` from `db/queries` |
| `make seed` | Load demo operator, playbooks and personas (Supabase when `DATABASE_URL` is set, else local Docker) |
| `make db-check` | Read-only: applied migration version + rows that would break migration 000005 |
| `make app-role` | Create or re-key the app login `buru_api`; prints its password once |
| `make keys` | Print fresh `SOCKABUS_OPERATOR_TOKEN` and `SOCKABUS_MESSAGE_KEY` values |
| `make tglogin PHONE=… PERSONA=…` | Log a honeypot number into Telegram and bind it |
| `make sim PERSONA=…` | Chat with a persona without Telegram (needs `make run`) |
| `make tgchat PHONE=… TO=…` | Play the scammer from your terminal |
| `make run` | Run the API + Temporal worker |
| `make web` | Run the dashboard |
| `make test` | `go test -race ./...` |
| `make test-db` | DB integration tests (needs `make up migrate-up`) |
| `make test-e2e` | End-to-end honeypot loop on local PostgreSQL + Temporal (never Supabase) |
| `make lint` | `go vet` + golangci-lint |
| `make arch` | Open the architecture diagram in the browser |
| `make deploy` | On the VPS: pull `main`, migrate Supabase, rebuild and restart the prod stack |
| `make backup` / `make restore-test` | `pg_dump` of Supabase into `backups/` / restore the newest dump into a throwaway container |

Database scripts run a pinned `postgres:17-alpine` client through Docker (`scripts/pg.sh`), so no local `psql` install is needed.

## Testing

```bash
make test                     # Go unit tests
make up migrate-up && make test-db test-e2e   # DB + Temporal integration (local Docker only)
pnpm install && pnpm exec playwright install chromium
SOCKABUS_API_URL=http://localhost:8080 SOCKABUS_WEB_URL=http://localhost:3000 \
SOCKABUS_OPERATOR_TOKEN=<backend token> pnpm test:e2e   # or test:api / test:ui
```

Playwright needs a backend in `SOCKABUS_MODE=live` with `SOCKABUS_SIM=1`, `SOCKABUS_LLM_PROVIDER=fake` and short reply timing. Point it at a scratch database, never Supabase. Full settings: [tests/README.md](tests/README.md). CI runs both projects on every PR (job `e2e` in `.github/workflows/ci.yml`).

## Project Structure

| Path | Purpose |
|---|---|
| `cmd/sockabus` | Entry point: HTTP server + Temporal worker |
| `internal/config` | Env-based config |
| `internal/server` | HTTP routes, health checks |
| `internal/workflows` | Temporal `ConversationWorkflow`: reply timing, typing, takeover (TR-3, TR-5) |
| `internal/gateway` | Channel-neutral inbound/outbound types and sender registry (TR-2) |
| `internal/channel/telegram` | Telegram connector on gotd/td (FR-2) |
| `internal/honeypot` | Live engine: inbound routing, workflow activities, operator actions, lookup |
| `internal/persona` | Persona system prompt, output guard, persona generator (TR-1) |
| `internal/playbook` | Playbook stage machine (TR-4) |
| `internal/llm` | LLM boundary: Gemini and Claude clients |
| `internal/msgcrypt` | Message encryption at rest (TR-14) |
| `cmd/tglogin` | One-time Telegram login for a honeypot number |
| `internal/extraction` | Entity extraction + scam classification (TR-6, TR-7) |
| `internal/tracer` | On-chain USDT tracing (TR-8) |
| `internal/intel` | Entity store, dedupe, verification (TR-9) |
| `internal/customerapi` | Lookup API, API keys, webhooks (TR-10, TR-12, TR-13) |
| `db/migrations` | PostgreSQL schema (golang-migrate) |
| `db/queries` | sqlc query files; `make sqlc` generates `internal/db` |
| `db/seed` | Demo operator, playbooks and personas (`make seed`) |
| `web` | Next.js Sockabus Console: `/personas`, `/conversations`, `/verify`, `/entities`, `/status`, `/demo/bank`, `/demo/chat`. `/api/*` proxies to the Go API |
| `internal/engine` | Honeypot loop: extract → classify → stage → persona reply |
| `internal/db` | sqlc-generated data layer |
| `internal/demostore` | In-memory data layer seeded from `seed.json` (to be replaced by PostgreSQL + Redis) |
| `tests` | Playwright API (`tests/api`) and UI (`tests/ui`) tests; config in `playwright.config.ts`, deps in root `package.json` |
| `scripts` | `pg.sh` (Dockerised psql/pg_dump), `backup.sh`, `restore-test.sh`, `app-role.sh`, `check-migrations.sql` |
| `docs` | PRD, TRD, contributing rules, architecture diagram (`docs/archify`) |

## Roadmap

- [ ] **M1 — Persona prototype:** Telegram connector, human-like reply timing (≥ 10 s, typing, typos)
- [ ] **M2 — Intelligence MVP:** persist personas, playbooks, sightings and verifications in PostgreSQL; LLM extraction
- [ ] **M3 — Customer pilot:** API keys + usage, batch lookup, Redis cache, p95 < 300 ms
- [ ] **M4 — Pilot-to-paid:** on-chain tracer (TronGrid / Etherscan), HMAC webhooks, WhatsApp, customer dashboard on live data

## Contributing

Branching, naming and PR rules are in [docs/Contributing_rules.md](docs/Contributing_rules.md). In short: branch from `dev` as `feature/*`, open a PR into `dev`; `main` only takes PRs from `dev`.

## License

MIT. See [LICENSE](LICENSE).

## Team

**Jaxz Tan Cheng Soo**
- GitHub: [@JaxzTan](https://github.com/JaxzTan)
- LinkedIn: [jaxz-tan-cheng-soo](https://www.linkedin.com/in/jaxz-tan-cheng-soo-a2b9b6361/)

**Gilbert Gan**
- GitHub: [@GGWX88](https://github.com/GGWX88)
- LinkedIn: [gilbert-gan](https://www.linkedin.com/in/gilbert-gan-362816401)

**Jayci Gan**
- GitHub: [@jaycigan05](https://github.com/jaycigan05)
- LinkedIn: [jayci-gan](https://www.linkedin.com/in/jayci-gan-b777472b9/)

**Hor Jian Jia**
- GitHub: [@hjianjia](https://github.com/hjianjia)
- LinkedIn: [hor-jian-jia](https://www.linkedin.com/in/jian-jia-hor-a08238404/)

**Andy Wong**
- GitHub: [@Andybunnny](https://github.com/Andybunnny)
- LinkedIn: [andy-wong-jian-dao](https://www.linkedin.com/in/andywongjiandao/)
