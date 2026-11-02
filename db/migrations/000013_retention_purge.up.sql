-- Controlled retention purge (TRD §7: raw messages kept 12 months). Additive: one new append-only
-- audit table, one new role, one new function, and buru_forbid_mutation() learns one exception.
--
-- What a purge deletes (rows older than the cutoff = now() - retention):
--   messages (sent_at < cutoff) and the entity_sightings rows that point at them (FK, 000001/000005);
--   conversation_summaries / persona_summaries / overview_summaries (created_at < cutoff): AI text
--   derived from those raw messages.
-- What it keeps: entities (intel, 24-month rule, not handled here) including sightings_count,
-- first_seen and last_seen; verifications (they reference entities, not messages); conversations,
-- stage events, health events and every other table.
--
-- How the append-only triggers are bypassed: only inside buru_purge_expired(), which sets the
-- transaction-local GUC buru.allow_purge = 'on' around its DELETEs. The trigger allows DELETE (never
-- UPDATE or TRUNCATE) only on the purgeable tables and only while that GUC is on. buru_app still has
-- no DELETE privilege on these tables (000002/000008), so setting the GUC itself gets it nothing.
-- EXECUTE is revoked from PUBLIC/buru_app and granted to the NOLOGIN role buru_purge only.

-- ─────────────── Audit trail (append-only) ───────────────

CREATE TABLE retention_purges (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    retention             interval    NOT NULL,
    cutoff                timestamptz NOT NULL, -- rows strictly older than this were (or would be) deleted
    dry_run               boolean     NOT NULL,
    messages_deleted      bigint      NOT NULL DEFAULT 0, -- dry run: rows that would be deleted
    sightings_deleted     bigint      NOT NULL DEFAULT 0,
    summaries_deleted     bigint      NOT NULL DEFAULT 0, -- conversation + persona + overview summaries
    requested_by          varchar(255) NOT NULL,        -- operator or job name, from the caller
    db_user               varchar(255) NOT NULL DEFAULT session_user,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX retention_purges_created_idx ON retention_purges (created_at DESC);

CREATE TRIGGER retention_purges_append_only BEFORE UPDATE OR DELETE ON retention_purges
    FOR EACH ROW EXECUTE FUNCTION buru_forbid_mutation();
CREATE TRIGGER retention_purges_no_truncate BEFORE TRUNCATE ON retention_purges
    FOR EACH STATEMENT EXECUTE FUNCTION buru_forbid_mutation();

-- ─────────────── Trigger: one narrow exception for the purge ───────────────

CREATE OR REPLACE FUNCTION buru_forbid_mutation() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE'
       AND TG_TABLE_NAME IN ('messages', 'entity_sightings', 'conversation_summaries', 'persona_summaries', 'overview_summaries')
       AND current_setting('buru.allow_purge', true) = 'on' THEN
        RETURN OLD; -- only buru_purge_expired() sets this, transaction-local
    END IF;
    RAISE EXCEPTION '% is append-only (NFR-8): % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

-- ─────────────── Purge function ───────────────

CREATE FUNCTION buru_purge_expired(retention interval DEFAULT interval '12 months',
                                   dry_run boolean DEFAULT true,
                                   requested_by text DEFAULT NULL)
    RETURNS retention_purges
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public, pg_temp
AS
$$
DECLARE
    v_cutoff    timestamptz;
    v_messages  bigint;
    v_sightings bigint;
    v_summaries bigint;
    v_n         bigint;
    v_row       retention_purges;
BEGIN
    -- Floor: TRD §7 keeps raw messages 12 months. A typo such as '12 days' must not wipe live data.
    IF retention IS NULL OR retention < interval '12 months' THEN
        RAISE EXCEPTION 'buru_purge_expired: retention % is below the 12-month minimum (TRD §7)', retention
            USING ERRCODE = 'invalid_parameter_value';
    END IF;
    IF dry_run IS NULL THEN
        RAISE EXCEPTION 'buru_purge_expired: dry_run must be true or false' USING ERRCODE = 'invalid_parameter_value';
    END IF;
    v_cutoff := now() - retention;

    IF dry_run THEN
        SELECT count(*) INTO v_messages FROM messages WHERE sent_at < v_cutoff;
        SELECT count(*) INTO v_sightings
        FROM entity_sightings s JOIN messages m ON m.id = s.message_id
        WHERE m.sent_at < v_cutoff;
        SELECT (SELECT count(*) FROM conversation_summaries WHERE created_at < v_cutoff)
             + (SELECT count(*) FROM persona_summaries WHERE created_at < v_cutoff)
             + (SELECT count(*) FROM overview_summaries WHERE created_at < v_cutoff)
        INTO v_summaries;
    ELSE
        -- Block concurrent inserts of sightings for the messages being deleted (FK would fail anyway).
        LOCK TABLE entity_sightings IN SHARE ROW EXCLUSIVE MODE;
        PERFORM set_config('buru.allow_purge', 'on', true);

        DELETE FROM entity_sightings s USING messages m
        WHERE m.id = s.message_id AND m.sent_at < v_cutoff;
        GET DIAGNOSTICS v_sightings = ROW_COUNT;

        DELETE FROM messages WHERE sent_at < v_cutoff;
        GET DIAGNOSTICS v_messages = ROW_COUNT;

        DELETE FROM conversation_summaries WHERE created_at < v_cutoff;
        GET DIAGNOSTICS v_summaries = ROW_COUNT;
        DELETE FROM persona_summaries WHERE created_at < v_cutoff;
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_summaries := v_summaries + v_n;
        DELETE FROM overview_summaries WHERE created_at < v_cutoff;
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_summaries := v_summaries + v_n;

        PERFORM set_config('buru.allow_purge', 'off', true);
    END IF;

    INSERT INTO retention_purges (retention, cutoff, dry_run, messages_deleted, sightings_deleted, summaries_deleted, requested_by, db_user)
    VALUES (retention, v_cutoff, dry_run, v_messages, v_sightings, v_summaries,
            left(COALESCE(NULLIF(btrim(requested_by), ''), session_user::text), 255), session_user)
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$$;

COMMENT ON FUNCTION buru_purge_expired(interval, boolean, text) IS
    'TRD §7 retention purge. Deletes messages older than now()-retention (min 12 months), their entity_sightings and old AI summaries; keeps entities and verifications; writes an audit row to retention_purges. Run via cmd/retention-purge (make retention-purge).';

-- ─────────────── Privileges ───────────────

DO $$
DECLARE
    r text;
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_purge') THEN
        CREATE ROLE buru_purge NOLOGIN;
    END IF;
    -- The app may read the audit trail but never write or change it (000003's default grants gave it more).
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_app') THEN
        REVOKE ALL ON retention_purges FROM buru_app;
        GRANT SELECT ON retention_purges TO buru_app;
        REVOKE ALL ON FUNCTION buru_purge_expired(interval, boolean, text) FROM buru_app;
    END IF;
    FOREACH r IN ARRAY ARRAY['anon', 'authenticated', 'service_role'] LOOP
        IF EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
            EXECUTE format('REVOKE ALL ON retention_purges FROM %I', r);
            EXECUTE format('REVOKE ALL ON FUNCTION buru_purge_expired(interval, boolean, text) FROM %I', r);
        END IF;
    END LOOP;
END
$$;

REVOKE ALL ON FUNCTION buru_purge_expired(interval, boolean, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION buru_purge_expired(interval, boolean, text) TO buru_purge;
