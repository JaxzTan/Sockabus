-- Phase 2 of 000012 (contract). PENDING: lives in db/migrations-pending, so `migrate up` does not apply it.
-- Apply only after: backfill-encrypt ran (real run) and `backfill-encrypt -verify` reports 0 missing and
-- 0 mismatched rows, and the app runs with SOCKABUS_IDENTIFIER_PLAINTEXT=0 (no more plaintext writes).
--
-- Changes existing data: clears the plaintext identifiers (the ciphertext copies stay). Irreversible
-- without the message key: the down migration cannot restore the plaintext (backfill-encrypt -restore does).

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM conversations
    WHERE (scammer_peer_ref IS NOT NULL AND scammer_peer_ref_bidx IS NULL)
       OR (scammer_handle IS NOT NULL AND scammer_handle_ciphertext IS NULL);
    IF n > 0 THEN
        RAISE EXCEPTION 'drop_plaintext_identifiers: % conversations have no ciphertext yet; run cmd/backfill-encrypt first', n;
    END IF;
    SELECT count(*) INTO n FROM personas WHERE demo_identifiers IS NOT NULL AND demo_identifiers_ciphertext IS NULL;
    IF n > 0 THEN
        RAISE EXCEPTION 'drop_plaintext_identifiers: % personas have no demo_identifiers ciphertext yet; run cmd/backfill-encrypt first', n;
    END IF;
END
$$;

UPDATE conversations SET scammer_handle = NULL, scammer_peer_ref = NULL
WHERE scammer_handle IS NOT NULL OR scammer_peer_ref IS NOT NULL;
UPDATE personas SET demo_identifiers = NULL WHERE demo_identifiers IS NOT NULL;

ALTER TABLE conversations
    ADD CONSTRAINT conversations_no_plaintext_identifiers_ck CHECK (scammer_handle IS NULL AND scammer_peer_ref IS NULL);
ALTER TABLE personas
    ADD CONSTRAINT personas_no_plaintext_demo_identifiers_ck CHECK (demo_identifiers IS NULL);

DROP INDEX IF EXISTS conversations_open_peer_uq;            -- 000006, replaced by conversations_open_peer_bidx_uq
DROP INDEX IF EXISTS conversations_open_peer_idx;           -- 000004
DROP INDEX IF EXISTS conversations_account_id_scammer_handle_idx; -- 000001
