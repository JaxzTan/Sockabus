# Progress: Sockabus against PRD v1.0 and TRD v1.0

| Field | Value |
|---|---|
| Author | Jaxz Tan |
| Last updated | 31 Oct 2026 |
| Branch checked | `feature/FS_V1` @ `2fc2e85` plus uncommitted work (see [explanation.md](explanation.md)) |
| Related | [PRD.md](PRD.md) · [TRD.md](TRD.md) · [explanation.md](explanation.md) · [architecture.html](architecture.html) |
| Target | **v1.0 bank demo, Mon 2 Nov 2026** |

## Summary

```
v1.0 demo path (7 steps)   █████████████████░░░  ~85%  code done; Supabase setup + rehearsal left
v1.0 infra (NFR-9/10/11)   ███████████░░░░░░░░░  ~55%  code done; VPS on hold
Release criteria (§13)      ░░░░░░░░░░░░░░░░░░░░   0/5  #1 and #4 reachable by Sunday; #2 and #3 need the VPS
```

Every backend gap from 26 Oct is now closed in code: `/internal/status`, Temporal in `/readyz`, the
Telegram heartbeat, a `fake` LLM provider, a configurable reply floor, Supabase-ready scripts, and
Playwright API + UI tests (9 tests, passing locally). What's left is running things: the Supabase
setup steps, the first CI run, a demo rehearsal, and everything that needs a server.

**The VPS is on hold.** Release criteria 2 (rehearse on the server) and 3 (Kuma green for 24 h) can't
be met by Sun 1 Nov. The demo will run from a laptop against Supabase.

Percentages are estimates from reading and running the code locally; nothing has run on GitHub CI or
a server yet.

## Workstreams

| Workstream | Progress | State | What's left |
|---|---|---|---|
| Endpoints | ~90% | Built | `api/openapi.yaml`; console fails when one conversation can't be decrypted |
| Frontend | ~90% | Wired, status page live | Same decrypt issue; UI test passes locally |
| Telegram connection | ~90% | Live locally, heartbeat built | Login on a server; G-9 replay after restart; G-10 ban status |
| DB | ~90% | All scripts target Supabase | Fix `.env` URLs; apply 000005; create `buru_api`; seed; backup + restore test |
| Vercel deployment | 0% | Not chosen | Only if the dashboard moves off the VPS |
| VPS deployment | ~55% | Prod stack ready for Supabase | Buy server, domain, `make deploy` (on hold) |
| Automate (Playwright) | ~85% | 9 tests pass locally; CI job written | First GitHub run; branch protection |
| Monitoring (cron job) | ~50% | Status, heartbeat, backup push, restore test built | Kuma monitors and alerts, backup cron (need the VPS) |

## v1.0 demo path (PRD §10)

| # | Demo step | Reqs | Progress | What exists | Missing for v1.0 |
|---|---|---|---|---|---|
| 1 | Generate a persona with one click | FR-15, FR-1 | ~95% | Endpoint, dashboard button, API test | < 30 s check with the live LLM |
| 2 | Teammate chats with the persona on Telegram (fallback: web chat) | FR-2, FR-3, FR-4 | ~90% | Telegram connector live, web chat page, ≥ 10 s floor, API + UI tests | Real Telegram test on demo day setup |
| 3 | Operator watches live and takes over | FR-5 | ~95% | Takeover, operator message, conversations page (3 s polling), API test | Rehearsal |
| 4 | Bank account / wallet appears as an entity | FR-6, FR-7 | ~95% | Extraction, classification, entity list, API + UI tests | Rehearsal |
| 5 | Operator verifies it | FR-9 | ~95% | Verify queue with Undo, append-only `verifications`, API test | Rehearsal |
| 6 | Bank simulator checks a transfer → held | FR-10 | ~95% | `/v1/lookup`, bank simulator page, API test (hit and miss) | Rehearsal |
| 7 | Status page shows bot + endpoints healthy | NFR-9 | ~80% | `/internal/status`, `/readyz` with Temporal, status page tiles | Uptime history and alerts (Kuma, needs the VPS) |

## v1.0 infrastructure

| ID | Requirement | Progress | What exists | Missing |
|---|---|---|---|---|
| NFR-9 / TR-20 | Monitoring, alert within 2 min | ~50% | `/internal/status`, `/readyz` (Postgres, Redis, Temporal), Telegram heartbeat to `SOCKABUS_KUMA_TG_PUSH_URL`, backup push | Kuma deployed, monitors, alerts to the team Telegram group |
| NFR-10 / TR-21 | Automated tests on every PR, merge blocked | ~85% | `fake` LLM, configurable reply floor, `playwright/playwright.config.ts`, `playwright/api` (8), `playwright/ui` (1), CI job `e2e` | First green CI run; branch protection on `main`; `api/openapi.yaml` |
| NFR-11 / TR-23 | 24/7 server, one-command deploy | ~55% | Prod compose without local Postgres, `make deploy` migrating Supabase, `make backup` / `restore-test` on Supabase | VPS, domain, server `.env`, backup cron |

## Release criteria (TRD §13, due Sun 1 Nov)

| # | Criterion | Status |
|---|---|---|
| 1 | All demo-path test cases pass in CI | ⏳ Pass locally (9/9, twice); CI job not run yet |
| 2 | Demo rehearsed twice on the deployed server | ⛔ No server; rehearse locally instead |
| 3 | Uptime Kuma green 24 h; forced bot stop alerts in < 2 min | ⛔ No server |
| 4 | Database backup taken and restore tested | ⏳ Scripts work locally; run `make backup && make restore-test` on Supabase |
| 5 | Repo tagged `v1.0.0` | ❌ After 1–4 |

## TRD §12 gaps

| # | Gap | In v1.0? | Status |
|---|---|---|---|
| G-6 | Regenerate `db/schema.dbml` | ✅ | Open (stale since 23 Oct) |
| G-7 | Operator TOTP + customer API keys | ❌ | Open |
| G-8 | KMS envelope encryption | ❌ | Open |
| G-9 | Persistent Telegram update state | ⚠ | Open |
| G-10 | Account health auto-status | ⚠ | Open; heartbeat loss covers alerting once Kuma runs |
| G-11 | Telegram connector live | ✅ | Done (commit `3ebbdcb`) |
| G-12 | Persona generation endpoint + button | ✅ | Done |
| G-13 | Audit of extraction, verify, lookup | ✅ | Covered by `playwright/api/demo-path.spec.ts` |

## Code vs TRD §5

The code works but differs from the TRD in three paths. Update the TRD, not the code:

| TRD §5 | Code |
|---|---|
| `POST /internal/dev/inject` behind `SOCKABUS_DEV_INJECT` | `POST /internal/sim/messages` behind `SOCKABUS_SIM` |
| `PATCH /internal/personas/{id}/status` | `POST /internal/personas/{id}/status` |
| `GET/POST /internal/conversations/{id}/messages` | `GET /internal/conversations/{id}` and `POST …/operator-message` |

## Post-v1.0 requirements

| ID | Requirement | Milestone | Progress | Notes |
|---|---|---|---|---|
| FR-8 | On-chain tracing | M4 | ~5% | Schema + mock UI |
| FR-10 (full) | Batch lookup, Redis cache, p95 < 300 ms | M3 | ~10% | `LookupEntitiesBatch` query exists, no endpoint |
| FR-11 (full) | Trend chart, CSV export | M4 | ~30% | Portal pages |
| FR-12 | Webhooks | M4 | ~5% | Schema + mock UI |
| FR-13 | API keys + usage | M3 | ~10% | Schema + sqlc queries (G-7) |
| FR-14 | Evidence reports | Stretch | 0% | — |
| NFR-1/2 | Lookup latency, 1,000 conversations | M3 | — | No load test (TR-15) |
| NFR-3 | TLS, hashed keys, rate limits | M3 | ~10% | Shared token |
| NFR-4 | Encryption at rest, retention | M3 | ~40% | AES-256-GCM single key (G-8) |
| NFR-5 | Legal review | M3 | 0% | Not started |
| NFR-6 | Multilingual personas | — | ~40% | Code-switching in prompts; no evals |
| NFR-8 | Tamper-proof audit trail | — | ~70% | Append-only triggers + grants; `make app-role` gives the app a restricted login |

## Path to the demo

1. **Sat 31 Oct, now:** review and commit; push; watch the first CI `e2e` run; branch protection (about 1 h)
2. **Sat afternoon:** Supabase: fix `.env` → `make db-check` → `make migrate-up` → `make app-role` → `make seed` → `make backup && make restore-test` (about 40 min)
3. **Sat night:** fix the console crash on an undecryptable conversation (about 1 h)
4. **Sun 1 Nov:** `make all` against Supabase, real Telegram test, rehearse twice (about 2.5 h)
5. **After the demo:** VPS, Kuma, backup cron, 24 h green, tag `v1.0.0`

The testing checklist (before commit, after push, before the demo) is in [explanation.md](explanation.md) §8.
