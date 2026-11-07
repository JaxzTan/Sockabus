-- Reverts the constraints and indexes of 000015. The plaintext values it cleared are NOT restored here;
-- run `go run ./cmd/backfill-encrypt -restore` (decrypts the ciphertext back into the plaintext columns)
-- before switching SOCKABUS_IDENTIFIER_PLAINTEXT back on or rolling back to a build without 000012 support.
ALTER TABLE personas DROP CONSTRAINT IF EXISTS personas_no_plaintext_demo_identifiers_ck;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_no_plaintext_identifiers_ck;

CREATE INDEX IF NOT EXISTS conversations_account_id_scammer_handle_idx ON conversations (account_id, scammer_handle);
CREATE INDEX IF NOT EXISTS conversations_open_peer_idx ON conversations (account_id, scammer_peer_ref) WHERE status <> 'closed';
CREATE UNIQUE INDEX IF NOT EXISTS conversations_open_peer_uq ON conversations (account_id, scammer_peer_ref) WHERE status <> 'closed';
