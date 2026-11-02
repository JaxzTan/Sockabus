-- Reverts 000013: the purge function, the trigger exception, the audit table. Roles are cluster-wide, so
-- buru_purge is dropped only when nothing in another database still depends on it.
DROP FUNCTION IF EXISTS buru_purge_expired(interval, boolean, text);

CREATE OR REPLACE FUNCTION buru_forbid_mutation() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    RAISE EXCEPTION '% is append-only (NFR-8): % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

DROP TABLE IF EXISTS retention_purges; -- the append-only triggers block DELETE/TRUNCATE, not DROP TABLE

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_purge') THEN
        DROP ROLE buru_purge;
    END IF;
EXCEPTION WHEN dependent_objects_still_exist THEN
    RAISE NOTICE 'buru_purge kept: objects in another database still depend on it';
END
$$;
