-- AI summaries (TR-7 "classifier on conversation summary", dashboard "Summary · today") and the
-- scam phase history of each conversation. All three tables are append-only (NFR-8): a new summary
-- or stage change is a new row, the latest row is the current one.
--
-- Two summary tables rather than one table with a subject type: each gets a real foreign key
-- (conversations / personas), its own "as of" counter and its own index, and nothing has to
-- interpret a polymorphic subject_id. The dashboard overview has no subject, so it has its own table.
-- Additive: new tables, indexes, triggers and grants only.

-- ─────────────── Conversation summaries ───────────────

CREATE TABLE conversation_summaries (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id  bigint       NOT NULL REFERENCES conversations (id),
    summary          text         NOT NULL,
    stage            varchar(32)  NOT NULL, -- conversations.stage when summarised
    scam_type        scam_type,             -- conversations.scam_type when summarised (NULL = unclassified)
    model            varchar(100) NOT NULL, -- e.g. anthropic/claude-haiku-5-5
    message_count_at int          NOT NULL, -- messages in the conversation when summarised
    reason           varchar(16)  NOT NULL DEFAULT 'on_demand'
        CHECK (reason IN ('stage_change', 'every_n', 'on_demand')),
    created_at       timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX conversation_summaries_conv_idx ON conversation_summaries (conversation_id, created_at DESC);

-- ─────────────── Persona summaries ───────────────

CREATE TABLE persona_summaries (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    persona_id            bigint       NOT NULL REFERENCES personas (id),
    summary               text         NOT NULL,
    model                 varchar(100) NOT NULL,
    conversation_count_at int          NOT NULL, -- the persona's conversations when summarised
    reason                varchar(16)  NOT NULL DEFAULT 'on_demand'
        CHECK (reason IN ('stage_change', 'every_n', 'on_demand')),
    created_at            timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX persona_summaries_persona_idx ON persona_summaries (persona_id, created_at DESC);

-- ─────────────── Dashboard overview (active personas + active chats) ───────────────

CREATE TABLE overview_summaries (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    summary              text         NOT NULL,
    model                varchar(100) NOT NULL,
    active_personas      int          NOT NULL,
    active_conversations int          NOT NULL,
    created_at           timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX overview_summaries_created_idx ON overview_summaries (created_at DESC);

-- ─────────────── Scam phase history ───────────────

-- One row per stage change (playbook machine, source 'bot'), scam-type change (source 'classifier')
-- or operator change (source 'operator'). The row at conversation start has from_stage NULL.
CREATE TABLE conversation_stage_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id bigint      NOT NULL REFERENCES conversations (id),
    from_stage      varchar(32),
    to_stage        varchar(32) NOT NULL,
    from_scam_type  scam_type,
    scam_type       scam_type,
    source          varchar(16) NOT NULL CHECK (source IN ('bot', 'operator', 'classifier')),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversation_stage_events_conv_idx ON conversation_stage_events (conversation_id, created_at, id);

-- ─────────────── Append-only enforcement + grants (as 000002 / 000003 / 000004) ───────────────

DO $$
DECLARE
    t text;
    r text;
BEGIN
    FOREACH t IN ARRAY ARRAY['conversation_summaries', 'persona_summaries', 'overview_summaries', 'conversation_stage_events'] LOOP
        EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION buru_forbid_mutation()', t || '_append_only', t);
        EXECUTE format('CREATE TRIGGER %I BEFORE TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION buru_forbid_mutation()', t || '_no_truncate', t);
        IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_app') THEN
            EXECUTE format('GRANT SELECT, INSERT ON %I TO buru_app', t);
            EXECUTE format('REVOKE UPDATE, DELETE, TRUNCATE ON %I FROM buru_app', t);
        END IF;
        FOREACH r IN ARRAY ARRAY['anon', 'authenticated'] LOOP
            IF EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
                EXECUTE format('REVOKE ALL ON %I FROM %I', t, r);
            END IF;
        END LOOP;
    END LOOP;
END
$$;
