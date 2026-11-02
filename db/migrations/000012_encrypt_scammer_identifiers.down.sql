-- Reverts 000012. scammer_handle gets NOT NULL back only if every row still has the plaintext
-- (true while the app dual-writes, i.e. before phase 2); otherwise this fails and changes nothing.
DROP INDEX IF EXISTS conversations_open_peer_bidx_uq;

ALTER TABLE personas
    DROP CONSTRAINT IF EXISTS personas_demo_identifiers_dek_ref_ck,
    DROP COLUMN IF EXISTS demo_identifiers_dek_ref,
    DROP COLUMN IF EXISTS demo_identifiers_ciphertext;

ALTER TABLE conversations
    DROP CONSTRAINT IF EXISTS conversations_peer_ref_bidx_ct_ck,
    DROP CONSTRAINT IF EXISTS conversations_identifiers_dek_ref_ck,
    DROP CONSTRAINT IF EXISTS conversations_peer_ref_bidx_len_ck,
    DROP COLUMN IF EXISTS identifiers_dek_ref,
    DROP COLUMN IF EXISTS scammer_peer_ref_bidx,
    DROP COLUMN IF EXISTS scammer_peer_ref_ciphertext,
    DROP COLUMN IF EXISTS scammer_handle_ciphertext,
    ALTER COLUMN scammer_handle SET NOT NULL;
