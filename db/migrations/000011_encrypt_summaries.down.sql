-- Reverts 000011. Rows written only as ciphertext (summary NULL) cannot go back to a NOT NULL plaintext
-- column, so they are deleted first: the append-only trigger is disabled for that one statement
-- (summaries are regenerated on demand, nothing references these rows).
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['conversation_summaries', 'persona_summaries', 'overview_summaries'] LOOP
        EXECUTE format('ALTER TABLE %I DISABLE TRIGGER %I', t, t || '_append_only');
        EXECUTE format('DELETE FROM %I WHERE summary IS NULL', t);
        EXECUTE format('ALTER TABLE %I ENABLE TRIGGER %I', t, t || '_append_only');
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT IF EXISTS %I, DROP CONSTRAINT IF EXISTS %I', t, t || '_body_ck', t || '_dek_ref_ck');
        EXECUTE format('ALTER TABLE %I ALTER COLUMN summary SET NOT NULL', t);
        EXECUTE format('ALTER TABLE %I DROP COLUMN IF EXISTS summary_ciphertext, DROP COLUMN IF EXISTS dek_ref', t);
    END LOOP;
END
$$;
