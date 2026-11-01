# Pending migrations (not applied by `migrate up`)

`migrate -path db/migrations` and sqlc read `db/migrations` only, so files here are never applied by
`make migrate-up`, `make deploy` or the e2e stack. Each one is a later phase of a rollout that must wait
for a manual step; move it into `db/migrations` (keeping the next free version number) once that step is done.

| File | Waits for | What it does |
|---|---|---|
| `000015_drop_plaintext_identifiers` | 000012 applied, app on the dual-write build, `cmd/backfill-encrypt` run and `-verify` clean, then `SOCKABUS_IDENTIFIER_PLAINTEXT=0` and app restarted | Refuses to run if any row still lacks ciphertext; then NULLs `conversations.scammer_handle`, `conversations.scammer_peer_ref` and `personas.demo_identifiers`, adds CHECKs so plaintext cannot come back, and drops the plaintext indexes. |

Renumber the file if `db/migrations` has meanwhile gained a 000015. Dropping the plaintext columns
themselves needs a code change (sqlc `SELECT *` lists them), so it is a separate, later migration.
