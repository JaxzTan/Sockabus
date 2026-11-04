# Backend endpoints

Every HTTP route served by the Go API (`cmd/sockabus`), with who can reach it.

Routes are registered in `internal/server/server.go` (`Routes`). The set depends on `SOCKABUS_MODE`:

- `live`: `internal/server/live.go` (`liveAPI.register`), backed by PostgreSQL, Telegram and Temporal
- `demo` (default): `internal/server/demo.go` (`registerDemo`), backed by in-memory pitch data

🤖 = the endpoint calls the Claude API (see [ai-usage.md](ai-usage.md)).

## Visibility levels

| Level | Who | Auth |
|---|---|---|
| **Public** | Anyone | None |
| **Customer API** | Paying customers (DaaS) and operators | `Authorization: Bearer sk_live_...` (customer API key, logged in `lookup_logs`) **or** the operator token (not logged) |
| **Internal** | Operators, through the web console | `Authorization: Bearer $SOCKABUS_OPERATOR_TOKEN` |
| **Dev only** | Local development (`cmd/simchat`, `make sim`) | Operator token. Mounted only when `SOCKABUS_SIM=1`, which is the default when `SOCKABUS_ENV=local` |

Internal requests have a 1 MiB body limit (`http.MaxBytesReader`).

### How the web console reaches the API

Browser → Next.js `/api/*` → Go API. The proxy (`web/src/app/api/[...path]/route.ts`) adds the operator token server-side, so the token never reaches the browser. `web/src/proxy.ts` requires a signed-in session for every `/api/*` call. The proxy also blocks `/api/v1/*`: the customer lookup goes only through `/api/demo/lookup`.

## Both modes

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/healthz` | Public | Liveness check: is the process running? Checks no dependencies |
| GET | `/readyz` | Public | Readiness: can it reach Postgres, Redis and Temporal? Returns 503 if one is down |

## Live mode (`SOCKABUS_MODE=live`)

### Customer API

| Method | Path | Visibility | Description |
|---|---|---|---|
| POST | `/v1/lookup` | Customer API | Look up an identifier (bank account, phone, handle) against collected scam data (FR-10) |

### Personas

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/internal/personas` | Internal | List personas |
| POST | `/internal/personas` | Internal | Create a persona |
| POST | `/internal/personas/generate` | Internal | 🤖 Generate personas with AI (Sonnet) |
| POST | `/internal/personas/{id}/status` | Internal | Set persona status (for example `active`) |
| POST | `/internal/personas/{id}/prompt` | Internal | Add a new prompt version |
| PUT | `/internal/personas/{id}/playbooks` | Internal | Set the persona's playbooks |
| GET | `/internal/personas/{id}/summary` | Internal | Get the stored persona AI summary |
| POST | `/internal/personas/{id}/summary` | Internal | 🤖 Generate a persona summary (Haiku) |
| GET | `/internal/playbooks` | Internal | List playbooks |

### Accounts (Telegram)

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/internal/accounts` | Internal | List bound messaging accounts |
| POST | `/internal/accounts` | Internal | Bind an account to a persona |
| POST | `/internal/accounts/{id}/connect` | Internal | Start or continue the Telegram login for an account |
| POST | `/internal/accounts/{id}/status` | Internal | Set account status |
| GET | `/internal/accounts/{id}/health` | Internal | Account health events |

### Conversations

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/internal/conversations` | Internal | List conversations |
| GET | `/internal/conversations/{id}` | Internal | Get one conversation with its messages |
| POST | `/internal/conversations/{id}/takeover` | Internal | Operator takes over from the bot |
| POST | `/internal/conversations/{id}/operator-message` | Internal | Send a message as the operator |
| POST | `/internal/conversations/{id}/close` | Internal | Close the conversation |
| GET | `/internal/conversations/{id}/summary` | Internal | Get the stored conversation AI summary |
| POST | `/internal/conversations/{id}/summary` | Internal | 🤖 Generate a conversation summary (Haiku) |
| GET | `/internal/conversations/{id}/stages` | Internal | Scam stage timeline |

### Overview

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/internal/summary` | Internal | Get the stored overview AI summary |
| POST | `/internal/summary` | Internal | 🤖 Generate an overview summary (Haiku) |
| GET | `/internal/status` | Internal | System status, including LLM errors from the last 15 minutes |

### Customers and API keys

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/internal/customers` | Internal | List customers |
| POST | `/internal/customers` | Internal | Create a customer |
| GET | `/internal/customers/{id}/keys` | Internal | List a customer's API keys |
| POST | `/internal/customers/{id}/keys` | Internal | Issue an API key (`sk_live_...`) |
| DELETE | `/internal/customers/{id}/keys/{keyId}` | Internal | Revoke an API key |
| GET | `/internal/customers/{id}/usage` | Internal | Customer lookup usage |

### Entities (extracted identifiers)

| Method | Path | Visibility | Description |
|---|---|---|---|
| GET | `/internal/entities` | Internal | List extracted identifiers |
| POST | `/internal/entities/{id}/verify` | Internal | Mark an identifier as verified |

### Chat simulator

| Method | Path | Visibility | Description |
|---|---|---|---|
| POST | `/internal/sim/accounts` | Dev only | Create a simulated account for a persona |
| POST | `/internal/sim/messages` | Dev only | 🤖 Send a fake scammer message (starts the AI reply flow) |
| GET | `/internal/sim/events` | Dev only | Long-poll (25s) for persona replies, typing and read receipts |

Messages from scammers that start the 🤖 reply, extraction and classification flow arrive through Telegram (`internal/channel/telegram`), not over HTTP.

## Demo mode (`SOCKABUS_MODE=demo`, default)

> ⚠️ **None of the demo routes check auth.** Every route below is effectively **public** if the port is reachable. Demo mode is for pitches and local use only, never for a public host.

| Method | Path | Visibility (by design) | Actual auth | Description |
|---|---|---|---|---|
| POST | `/v1/lookup` | Customer API | None | Look up an identifier |
| GET | `/v1/entities` | Customer API | None | Verified entities (customer view) |
| GET | `/v1/weeks` | Customer API | None | Weekly stats |
| GET | `/v1/usage` | Customer API | None | Daily usage |
| GET | `/internal/entities` | Internal | None | All entities (operator view) |
| GET | `/internal/weeks` | Internal | None | Weekly stats |
| GET | `/internal/playbooks` | Internal | None | List playbooks |
| GET | `/internal/conversations` | Internal | None | List conversations |
| GET | `/internal/personas` | Internal | None | List personas |
| GET | `/internal/accounts` | Internal | None | List accounts |
| POST | `/internal/entities/{id}/verify` | Internal | None | Verify an entity |
| POST | `/internal/conversations/{id}/takeover` | Internal | None | Operator takeover |
| POST | `/internal/conversations/{id}/close` | Internal | None | Close the conversation |
| POST | `/internal/conversations/{id}/operator-message` | Internal | None | Send an operator message |
| POST | `/internal/conversations/{id}/inbound` | Internal | None | 🤖 Inject a scammer message (Sonnet reply if `ANTHROPIC_API_KEY` is set) |
| POST | `/internal/extract` | Internal | None | Run regex extraction on text |
| POST | `/internal/personas` | Internal | None | Create a persona |
| POST | `/internal/accounts` | Internal | None | Add an account |
| POST | `/internal/accounts/{id}/status` | Internal | None | Set account status |
| DELETE | `/internal/accounts/{id}` | Internal | None | Remove an account |

## Notes

- `/readyz` is public and returns raw dependency error strings (`err.Error()`) when a check fails. Those strings can include internal hostnames.
- `/internal/*` is meant to sit behind the Next.js proxy. Exposing the Go port directly is safe in live mode only while `SOCKABUS_OPERATOR_TOKEN` stays secret, because that one shared token authorizes every internal route.
- If `SOCKABUS_OPERATOR_TOKEN` is empty, every Internal route returns 401 (it never fails open).
