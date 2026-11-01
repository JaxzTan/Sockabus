#!/usr/bin/env bash
# Creates (or re-keys) the app's login role buru_api as a member of buru_app (migration 000002), so the
# app runs without UPDATE/DELETE on the append-only tables (NFR-8). Run with the owner DATABASE_URL
# (make app-role). Prints the new SOCKABUS_POSTGRES_URL password once; it is stored nowhere else.
set -euo pipefail

password=$(openssl rand -hex 24)
# The password goes through stdin, never the command line.
"$(dirname "$0")/pg.sh" psql -v ON_ERROR_STOP=1 -q <<SQL
SET client_min_messages = warning;
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_app') THEN
        RAISE EXCEPTION 'role buru_app is missing: run make migrate-up first';
    END IF;
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_api') THEN
        ALTER ROLE buru_api WITH LOGIN PASSWORD '$password';
    ELSE
        CREATE ROLE buru_api WITH LOGIN PASSWORD '$password' IN ROLE buru_app;
    END IF;
END
\$\$;
GRANT buru_app TO buru_api;
SQL
echo "buru_api is ready. Put this password in SOCKABUS_POSTGRES_URL (user buru_api.<project-ref> on the Supabase pooler):"
echo "$password"
