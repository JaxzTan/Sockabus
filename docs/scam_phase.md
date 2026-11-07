# Scam Phases

Every honeypot conversation moves through 4 scam phases (playbook stages). The phases come from TR-4 / FR-4 in [TRD.md](TRD.md) and [PRD.md](PRD.md).

```
rapport → bait → payment_request → stall
```

Phases only move forward. A conversation never goes back to an earlier phase.

## The 4 phases

| # | Key | Persona goal | Generic example |
|---|-----|--------------|-----------------|
| 1 | `rapport` | Act like a friendly, slightly cautious real person. Ask who the scammer is and what this is about. | `hello.. who is this ah?` |
| 2 | `bait` | Show interest and ask how it works. Let the scammer explain the offer in full. | `ok can explain again? how it works ah` |
| 3 | `payment_request` | Agree, but hesitantly. Ask exactly where to send money: bank, account number, holder name, or wallet address and network. | `ok la.. which account ah? name what?` |
| 4 | `stall` | Say the transfer failed or you are waiting for someone. Ask for another account or wallet. | `bank say cannot transfer to this one, got other account?` |

The generic stages live in `internal/playbook/playbook.go` (`Generic`). They are used when a conversation has no playbook yet.

## When a phase changes

`playbook.Next` (`internal/playbook/playbook.go`) checks each new scammer message. The highest matching rule wins:

| Signal on the latest scammer message | Target phase |
|--------------------------------------|--------------|
| A bank account or wallet was extracted | `stall` (we have one account, so stall to get a second one) |
| The scammer asks the persona to pay (`AsksForPayment`) | `payment_request` |
| 2 or more scammer messages so far | `bait` |
| Otherwise | `rapport` |

The conversation moves only when the target phase comes after the current one.

`AsksForPayment` matches payment words in English, Malay and Chinese: `transfer`, `top up`, `bank in`, `fee`, `bayar`, `akaun`, `转账`, `付款`, and others. Words about the scammer's own fake returns (`withdraw`, `profit`, `release`) do not count. Those belong to the bait phase.

## Playbooks per scam type

Each scam type has its own playbook with the same 4 phases. Each playbook sets its own goal and example line for every phase. They are seeded in `db/seed/001_demo.sql`.

| Scam type | Rapport | Bait | Payment request | Stall |
|-----------|---------|------|-----------------|-------|
| `job` | Eager for side income, mentions money worry | Do the first "free" task, accept payout | Ask which account and confirm the name | Excuses to draw a second account |
| `investment` | Curious but cautious, idle savings | Ask for proof of returns | Ask for wallet and network, or bank option | Exchange "blocked" it, ask for another wallet |
| `parcel` | Confirms online orders often | Try the link, say it "failed" | Ask to pay by transfer instead | Delay to collect alternate accounts |
| `impersonation` | Worried, asks who is calling | Slowly follow "verification" steps | Ask for "safe account" details twice | Bank "flagged" it, ask for another account |
| `romance` | Warm, shares light personal details | Accept gifts or parcel story | Ask for exact account for the fee | Family concern, ask for another way to pay |
| `loan` | Needs a small loan urgently | Over-cautious questions on interest and approval | Ask which account for the processing fee | Transfer "rejected", ask for another account |

## What happens on a phase change

The flow is in `ProcessInbound` (`internal/honeypot/activities.go`):

1. `conversations.stage` is updated and one row is added to `conversation_stage_events`, in the same transaction (`setStage`, `internal/honeypot/events.go`).
2. The scam-type classifier runs again (TR-7).
3. The AI conversation summary refreshes in the background, with reason `stage_change`.

The persona's next reply uses the current phase's goal and example in its system prompt. If the LLM fails, it falls back to the example line.

## Phase history

`conversation_stage_events` (migration `000008`) is append-only. Each row has:

- `from_stage` / `to_stage`: `from_stage` is `NULL` on the first row, when the conversation starts.
- `from_scam_type` / `scam_type`
- `source`: `bot` (playbook machine), `classifier` (scam-type change) or `operator` (manual change)

Read it with `GET /internal/conversations/{id}/stages` (see [endpoints.md](endpoints.md)).

## Success metric

At least 40% of conversations should reach `payment_request` (PRD Goal 2, FR-4). Payment details captured there are how we find mule accounts and wallets.
