-- AI summaries encrypted at rest (TRD §7 "encrypted at rest", NFR-4): summaries quote what the scammer
-- said (bank names, account hints), so they get the same AES-GCM sealing as messages (internal/msgcrypt).
-- AAD binds each ciphertext to its table and subject: "conv_summary:<conversation_id>",
-- "persona_summary:<persona_id>", "overview_summary".
--
-- Additive and safe with existing rows: two nullable columns, `summary` becomes nullable, and a CHECK that
-- every row still has one of the two. Existing plaintext rows are NOT rewritten (the tables are
-- append-only, so they cannot be UPDATEd); the app reads ciphertext first and falls back to `summary`.
-- New rows are written with summary_ciphertext + dek_ref and summary NULL.
-- The plaintext column is kept (not dropped) so an app still running the previous build during the
-- deploy window keeps working, and old plaintext rows stay readable until retention removes them.
ALTER TABLE conversation_summaries
    ADD COLUMN summary_ciphertext bytea,
    ADD COLUMN dek_ref            varchar(255),
    ALTER COLUMN summary DROP NOT NULL,
    ADD CONSTRAINT conversation_summaries_body_ck CHECK (summary IS NOT NULL OR summary_ciphertext IS NOT NULL),
    ADD CONSTRAINT conversation_summaries_dek_ref_ck CHECK ((summary_ciphertext IS NULL) = (dek_ref IS NULL));

ALTER TABLE persona_summaries
    ADD COLUMN summary_ciphertext bytea,
    ADD COLUMN dek_ref            varchar(255),
    ALTER COLUMN summary DROP NOT NULL,
    ADD CONSTRAINT persona_summaries_body_ck CHECK (summary IS NOT NULL OR summary_ciphertext IS NOT NULL),
    ADD CONSTRAINT persona_summaries_dek_ref_ck CHECK ((summary_ciphertext IS NULL) = (dek_ref IS NULL));

ALTER TABLE overview_summaries
    ADD COLUMN summary_ciphertext bytea,
    ADD COLUMN dek_ref            varchar(255),
    ALTER COLUMN summary DROP NOT NULL,
    ADD CONSTRAINT overview_summaries_body_ck CHECK (summary IS NOT NULL OR summary_ciphertext IS NOT NULL),
    ADD CONSTRAINT overview_summaries_dek_ref_ck CHECK ((summary_ciphertext IS NULL) = (dek_ref IS NULL));
