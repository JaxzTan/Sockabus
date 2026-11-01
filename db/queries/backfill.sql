-- cmd/backfill-encrypt (000012 phase 1 → 2): fills the ciphertext / blind-index columns for rows written
-- before the app encrypted scammer identifiers, verifies them, and (-restore) undoes phase 2.

-- name: ListConversationsMissingCiphertext :many
SELECT id, account_id, scammer_handle, scammer_peer_ref FROM conversations
WHERE (scammer_peer_ref IS NOT NULL AND scammer_peer_ref_ciphertext IS NULL)
   OR (scammer_handle IS NOT NULL AND scammer_handle_ciphertext IS NULL)
ORDER BY id;

-- name: SetConversationIdentifiersCiphertext :execrows
-- Idempotent: only columns that are still NULL are filled, so a rerun or a row written by the app
-- in the meantime is left as it is.
UPDATE conversations
SET scammer_handle_ciphertext   = COALESCE(scammer_handle_ciphertext, sqlc.narg(handle_ciphertext)),
    scammer_peer_ref_ciphertext = COALESCE(scammer_peer_ref_ciphertext, sqlc.narg(peer_ref_ciphertext)),
    scammer_peer_ref_bidx       = CASE WHEN scammer_peer_ref_ciphertext IS NULL THEN sqlc.narg(peer_ref_bidx) ELSE scammer_peer_ref_bidx END,
    identifiers_dek_ref         = COALESCE(identifiers_dek_ref, sqlc.narg(dek_ref))
WHERE id = sqlc.arg(id)
  AND ((scammer_handle_ciphertext IS NULL AND sqlc.narg(handle_ciphertext)::bytea IS NOT NULL)
    OR (scammer_peer_ref_ciphertext IS NULL AND sqlc.narg(peer_ref_ciphertext)::bytea IS NOT NULL));

-- name: ListConversationIdentifiers :many
SELECT id, account_id, status, scammer_handle, scammer_peer_ref,
       scammer_handle_ciphertext, scammer_peer_ref_ciphertext, scammer_peer_ref_bidx, identifiers_dek_ref
FROM conversations ORDER BY id;

-- name: RestoreConversationPlaintextIdentifiers :execrows
UPDATE conversations SET scammer_handle = $2, scammer_peer_ref = $3
WHERE id = $1 AND scammer_handle IS NULL AND scammer_peer_ref IS NULL;

-- name: ListPersonasMissingCiphertext :many
SELECT id, demo_identifiers FROM personas
WHERE demo_identifiers IS NOT NULL AND demo_identifiers_ciphertext IS NULL
ORDER BY id;

-- name: ListPersonaDemoIdentifiers :many
SELECT id, demo_identifiers, demo_identifiers_ciphertext, demo_identifiers_dek_ref FROM personas ORDER BY id;

-- name: RestorePersonaPlaintextDemoIdentifiers :execrows
UPDATE personas SET demo_identifiers = $2 WHERE id = $1 AND demo_identifiers IS NULL;

-- name: CountPlaintextSummaries :one
-- Summaries written before 000011 stay plaintext (append-only: they cannot be UPDATEd). Reported only.
SELECT (SELECT count(*) FROM conversation_summaries WHERE summary_ciphertext IS NULL)
     + (SELECT count(*) FROM persona_summaries WHERE summary_ciphertext IS NULL)
     + (SELECT count(*) FROM overview_summaries WHERE summary_ciphertext IS NULL) AS plaintext_rows;

-- name: RecentMessagesForKeyCheck :many
-- backfill-encrypt proves its SOCKABUS_MESSAGE_KEY is the app's key by opening message bodies the app sealed.
SELECT id, conversation_id, body_ciphertext, dek_ref FROM messages ORDER BY id DESC LIMIT 20;
