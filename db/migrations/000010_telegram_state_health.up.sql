-- Telegram update state (TRD G-9) and honeypot account health history (TRD G-10, TR-17).
-- Additive: new tables, index, triggers and grants only.

-- gotd/td updates.StateStorage common state for one honeypot account, so a restart can ask Telegram
-- for the difference since the last seen pts/qts instead of losing messages sent while down.
CREATE TABLE telegram_update_state (
    account_id bigint      PRIMARY KEY REFERENCES honeypot_accounts (id),
    pts        int         NOT NULL DEFAULT 0,
    qts        int         NOT NULL DEFAULT 0,
    date       int         NOT NULL DEFAULT 0, -- Telegram's unix time of the state
    seq        int         NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only log of what happened to an account: connector events and status changes.
CREATE TABLE account_health_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES honeypot_accounts (id),
    event      varchar(32) NOT NULL CHECK (event IN (
        'connected', 'disconnected', 'connect_error', 'unauthorized', 'banned', 'flood_wait', 'cooldown', 'active')),
    detail     jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_health_events_account_idx ON account_health_events (account_id, created_at DESC);

CREATE TRIGGER account_health_events_append_only BEFORE UPDATE OR DELETE ON account_health_events
    FOR EACH ROW EXECUTE FUNCTION buru_forbid_mutation();
CREATE TRIGGER account_health_events_no_truncate BEFORE TRUNCATE ON account_health_events
    FOR EACH STATEMENT EXECUTE FUNCTION buru_forbid_mutation();

DO $$
DECLARE
    r text;
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_app') THEN
        GRANT SELECT, INSERT, UPDATE ON telegram_update_state TO buru_app;
        GRANT SELECT, INSERT ON account_health_events TO buru_app;
        REVOKE UPDATE, DELETE, TRUNCATE ON account_health_events FROM buru_app;
    END IF;
    FOREACH r IN ARRAY ARRAY['anon', 'authenticated'] LOOP
        IF EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
            EXECUTE format('REVOKE ALL ON telegram_update_state FROM %I', r);
            EXECUTE format('REVOKE ALL ON account_health_events FROM %I', r);
        END IF;
    END LOOP;
END
$$;
