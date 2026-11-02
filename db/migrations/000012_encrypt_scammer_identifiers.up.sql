-- Scammer identifiers encrypted at rest (TRD §7), phase 1 of 2 (expand). Additive only:
--   conversations.scammer_peer_ref  → scammer_peer_ref_ciphertext + scammer_peer_ref_bidx (keyed blind index)
--   conversations.scammer_handle    → scammer_handle_ciphertext
--   personas.demo_identifiers       → demo_identifiers_ciphertext
-- Ciphertext is AES-256-GCM (internal/msgcrypt). The blind index is HMAC-SHA256 under a key derived
-- from SOCKABUS_MESSAGE_KEY with HKDF (separate purpose label), so routing can match a peer by equality
-- without storing it in clear. AAD: "conv_peer_ref:acct:<account_id>", "conv_handle:acct:<account_id>",
-- "persona_demo_ids:<persona_id>".
--
-- Rollout: the app writes old and new columns and reads new-with-fallback-to-old; `cmd/backfill-encrypt`
-- fills the new columns for existing rows (no UPDATE here); db/migrations-pending/*_drop_plaintext_identifiers
-- clears the plaintext once the backfill is verified.
--
-- scammer_handle loses NOT NULL so that, after phase 2, rows can be written without plaintext. Existing
-- rows and the previous app build are unaffected (they always write it). New CHECKs only constrain the
-- new columns, which are NULL on every existing row. The new unique index is built on NULLs only.

ALTER TABLE conversations
    ADD COLUMN scammer_handle_ciphertext   bytea,
    ADD COLUMN scammer_peer_ref_ciphertext bytea,
    ADD COLUMN scammer_peer_ref_bidx       bytea, -- HMAC-SHA256(index key, "peer_ref:" || peer ref), 32 bytes
    ADD COLUMN identifiers_dek_ref         varchar(255),
    ALTER COLUMN scammer_handle DROP NOT NULL,
    ADD CONSTRAINT conversations_peer_ref_bidx_len_ck CHECK (scammer_peer_ref_bidx IS NULL OR octet_length(scammer_peer_ref_bidx) = 32),
    ADD CONSTRAINT conversations_identifiers_dek_ref_ck
        CHECK ((scammer_handle_ciphertext IS NULL AND scammer_peer_ref_ciphertext IS NULL) OR identifiers_dek_ref IS NOT NULL),
    ADD CONSTRAINT conversations_peer_ref_bidx_ct_ck
        CHECK ((scammer_peer_ref_bidx IS NULL) = (scammer_peer_ref_ciphertext IS NULL));

-- Routing (FindOpenConversationByPeer) and the one-open-conversation-per-peer rule (000006) move to
-- the blind index. conversations_open_peer_uq (plaintext) stays until phase 2 drops it.
CREATE UNIQUE INDEX conversations_open_peer_bidx_uq
    ON conversations (account_id, scammer_peer_ref_bidx)
    WHERE status <> 'closed' AND scammer_peer_ref_bidx IS NOT NULL;

ALTER TABLE personas
    ADD COLUMN demo_identifiers_ciphertext bytea,
    ADD COLUMN demo_identifiers_dek_ref    varchar(255),
    ADD CONSTRAINT personas_demo_identifiers_dek_ref_ck
        CHECK ((demo_identifiers_ciphertext IS NULL) = (demo_identifiers_dek_ref IS NULL));
