-- AI summaries (TR-7) and scam phase history (migration 000008). All four tables are append-only.

-- name: InsertConversationSummary :one
-- summary (plaintext) is left NULL: the text is stored sealed in summary_ciphertext (000011).
INSERT INTO conversation_summaries (conversation_id, summary_ciphertext, dek_ref, stage, scam_type, model, message_count_at, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: LatestConversationSummary :one
SELECT * FROM conversation_summaries
WHERE conversation_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: InsertPersonaSummary :one
INSERT INTO persona_summaries (persona_id, summary_ciphertext, dek_ref, model, conversation_count_at, reason)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: LatestPersonaSummary :one
SELECT * FROM persona_summaries
WHERE persona_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: InsertOverviewSummary :one
INSERT INTO overview_summaries (summary_ciphertext, dek_ref, model, active_personas, active_conversations)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: LatestOverviewSummary :one
SELECT * FROM overview_summaries ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: CountMessages :one
SELECT count(*) FROM messages WHERE conversation_id = $1;

-- name: ListPersonaConversations :many
-- A persona's conversations, newest activity first (persona summary input).
SELECT c.* FROM conversations c
JOIN honeypot_accounts a ON a.id = c.account_id
WHERE a.persona_id = $1
ORDER BY COALESCE(c.last_message_at, c.started_at) DESC
LIMIT $2;

-- name: CountPersonaConversations :one
SELECT count(*) FROM conversations c
JOIN honeypot_accounts a ON a.id = c.account_id
WHERE a.persona_id = $1;

-- name: ListActiveConversationsWithLatestSummary :many
-- Dashboard overview input: open conversations with their newest summary (if any).
SELECT c.id, c.stage, c.scam_type, c.status, c.started_at, c.last_message_at, p.id AS persona_id, p.name AS persona_name,
       s.summary, s.summary_ciphertext, s.summary_dek_ref
FROM conversations c
JOIN honeypot_accounts a ON a.id = c.account_id
JOIN personas p ON p.id = a.persona_id
LEFT JOIN LATERAL (
    SELECT cs.summary, cs.summary_ciphertext, cs.dek_ref AS summary_dek_ref FROM conversation_summaries cs
    WHERE cs.conversation_id = c.id
    ORDER BY cs.created_at DESC, cs.id DESC
    LIMIT 1
) s ON true
WHERE c.status <> 'closed'
ORDER BY COALESCE(c.last_message_at, c.started_at) DESC
LIMIT $1;

-- name: InsertStageEvent :one
INSERT INTO conversation_stage_events (conversation_id, from_stage, to_stage, from_scam_type, scam_type, source)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListStageEvents :many
SELECT * FROM conversation_stage_events
WHERE conversation_id = $1
ORDER BY created_at, id;
