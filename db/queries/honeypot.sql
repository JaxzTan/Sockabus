-- Live honeypot queries (FR-2 routing, persona generator, operator console) that build on the
-- core queries in conversations.sql, entities.sql and personas.sql. Uses columns from
-- migration 000004 (scammer_peer_ref, dedupe_key, personas.profile/demo_identifiers).

-- name: FindOpenConversationByPeer :one
-- Routes an inbound message to its open conversation by channel reply address (FR-2). Matches the
-- keyed blind index (000012); rows not yet backfilled (bidx NULL) fall back to the plaintext column.
SELECT * FROM conversations
WHERE account_id = sqlc.arg(account_id) AND status <> 'closed'
  AND (scammer_peer_ref_bidx = sqlc.arg(peer_bidx)::bytea
       OR (scammer_peer_ref_bidx IS NULL AND scammer_peer_ref = sqlc.arg(peer_ref)::text))
ORDER BY started_at DESC
LIMIT 1;

-- name: CreateConversationForPeer :one
-- Returns no row when another open conversation for the same (account, peer) won the race
-- (conversations_open_peer_bidx_uq from 000012, or conversations_open_peer_uq from 000006 while the
-- plaintext is still written); the caller then re-selects with FindOpenConversationByPeer.
-- scammer_handle / scammer_peer_ref are NULL once SOCKABUS_IDENTIFIER_PLAINTEXT=0 (phase 2).
INSERT INTO conversations (account_id, playbook_id, temporal_workflow_id, scammer_handle, scammer_peer_ref, started_at,
                           scammer_handle_ciphertext, scammer_peer_ref_ciphertext, scammer_peer_ref_bidx, identifiers_dek_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: SetConversationPlaybook :exec
UPDATE conversations SET playbook_id = $2 WHERE id = $1;

-- name: ListConversationsWithPersona :many
-- Operator console list; NULL status = all.
SELECT c.*, a.persona_id, a.channel, p.name AS persona_name
FROM conversations c
JOIN honeypot_accounts a ON a.id = c.account_id
JOIN personas p ON p.id = a.persona_id
WHERE (sqlc.narg(status)::conversation_status IS NULL OR c.status = sqlc.narg(status))
ORDER BY COALESCE(c.last_message_at, c.started_at) DESC
LIMIT sqlc.arg(row_limit);

-- name: InsertMessageDeduped :one
-- Like InsertMessage, but idempotent on dedupe_key: returns no row when the key already exists
-- (a redelivered channel update or a retried send). Also bumps conversations.last_message_at.
WITH m AS (
    INSERT INTO messages (conversation_id, direction, sender_type, operator_id, body_ciphertext, dek_ref, sent_at, dedupe_key)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    ON CONFLICT (dedupe_key) DO NOTHING
    RETURNING *
), c AS (
    UPDATE conversations SET last_message_at = (SELECT sent_at FROM m) WHERE id = (SELECT conversation_id FROM m)
)
SELECT * FROM m;

-- name: GetMessage :one
SELECT * FROM messages WHERE id = $1;

-- name: ListRecentMessages :many
-- Newest first; callers reverse into chronological order.
SELECT * FROM messages
WHERE conversation_id = $1
ORDER BY sent_at DESC, id DESC
LIMIT $2;

-- name: CountInboundMessages :one
SELECT count(*) FROM messages WHERE conversation_id = $1 AND direction = 'inbound';

-- name: FirstInboundMessageID :one
SELECT id FROM messages
WHERE conversation_id = $1 AND direction = 'inbound'
ORDER BY sent_at, id
LIMIT 1;

-- name: ListConversationEntities :many
SELECT DISTINCT e.* FROM entities e
JOIN entity_sightings s ON s.entity_id = e.id
WHERE s.conversation_id = $1
ORDER BY e.id;

-- name: CreatePersonaWithProfile :one
-- Full insert used by the operator API and the persona generator. demo_identifiers is the plaintext
-- copy (NULL once SOCKABUS_IDENTIFIER_PLAINTEXT=0); the ciphertext is set by SetPersonaDemoIdentifiersCiphertext
-- in the same transaction, because its AAD needs the new persona id.
INSERT INTO personas (name, age, language_mix, backstory, wealth_profile, status, profile, demo_identifiers)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: SetPersonaDemoIdentifiersCiphertext :execrows
-- Idempotent (backfill-encrypt): never overwrites an existing ciphertext.
UPDATE personas SET demo_identifiers_ciphertext = $2, demo_identifiers_dek_ref = $3
WHERE id = $1 AND demo_identifiers_ciphertext IS NULL;

-- name: ListPersonasForDedupe :many
-- Dedup memory for the persona generator: every persona not retired.
SELECT name, age, profile FROM personas WHERE status <> 'retired' ORDER BY id;

-- name: DetachPlaybooks :exec
DELETE FROM persona_playbooks WHERE persona_id = $1;

-- name: GetPlaybook :one
SELECT * FROM playbooks WHERE id = $1;
