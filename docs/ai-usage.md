# AI usage (Claude API)

Every place the app calls the Claude API with `ANTHROPIC_API_KEY`, and which model each one uses.

## Models

| Env var | Default model | Used for |
|---|---|---|
| `SOCKABUS_PERSONA_MODEL` | `claude-sonnet-5-5` | Live persona replies, persona generation |
| `SOCKABUS_EXTRACT_MODEL` | `claude-haiku-5-5` | Extraction, classification, AI summaries |
| `SOCKABUS_MODEL` | `claude-sonnet-5-5` (when empty) | Demo-mode persona replies (separate client in `internal/persona/persona.go`) |

`SOCKABUS_LLM_PROVIDER` selects the provider: `anthropic` (default), `gemini`, `fake` (CI) or `none`.
Clients are wired in `cmd/sockabus/main.go` (`wireLive`).

## Call sites

| # | Feature | Model | Code | When it fires | Retries / timeout |
|---|---|---|---|---|---|
| 1 | Persona chat reply (live) | **Sonnet** | `internal/honeypot/activities.go:314` | Every bot reply to a scammer | 3 tries × 14s (`SOCKABUS_LLM_TRY_TIMEOUT`), 45s total cap (`SOCKABUS_LLM_TIMEOUT`) |
| 2 | Persona generation (Generate button) | **Sonnet** | `internal/honeypot/admin.go:195` → `internal/persona/generator.go:131` | Each generate request | 2 tries × 90s (`SOCKABUS_PERSONA_GEN_TIMEOUT`) |
| 3 | Demo-mode persona reply | **Sonnet** | `internal/engine/engine.go:128` | Every reply in demo mode, including the auto simulator | Single call, 20s HTTP timeout |
| 4 | Identifier extraction (bank accounts, phones, handles) | Haiku | `internal/extraction/llm.go:43` (called from `activities.go:81`) | Every inbound scammer message | 2 tries × 9s |
| 5 | Scam-type classification | Haiku | `internal/extraction/llm.go:142` (called from `activities.go:205`) | Per conversation classification | 2 tries × 9s |
| 6 | Conversation summary | Haiku | `internal/honeypot/summary.go:211` | On summary request | 2 tries × 90s |
| 7 | Persona summary | Haiku | `internal/honeypot/summary.go:320` | On summary request | 2 tries × 90s |
| 8 | Overview summary | Haiku | `internal/honeypot/summary.go:396` | On summary request | 2 tries × 90s |

## Fallbacks

- **Summaries** use the first client that is set: `SummaryLLM` (Haiku) → `GenLLM` (Sonnet) → `PersonaLLM` (Sonnet). See `summary.go:126`. If no extract model is configured, summaries run on Sonnet.
- **Persona generation** uses `GenLLM`, or `PersonaLLM` when that is nil (`admin.go:195`).
- **No LLM** (`SOCKABUS_LLM_PROVIDER=none` or no key): replies use playbook example lines, extraction uses regex, classification uses keywords.

## Cost notes

- **Demo simulator spends Sonnet tokens with no one using the app.** In demo mode (`SOCKABUS_MODE=demo`, the default), `WithDemo` starts a scripted scammer simulator (`internal/engine/sim.go:39`): 6 conversations by default (`SOCKABUS_SIM_MAX`), and Sonnet replies to every line. Set `SOCKABUS_SIMULATE=0` to disable it.
- **Retries multiply cost.** One slow persona reply can become 3 Sonnet calls, and one persona generation can become 2.
- **Per live conversation:** 1 Sonnet call per bot reply, plus 1 Haiku call per scammer message, plus Haiku calls for classification and summaries.
- Failed calls (after retries) are counted over a 15-minute window and shown at `GET /internal/status`.
