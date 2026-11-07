#!/usr/bin/env bash
# Runs a PostgreSQL client (psql, pg_dump) against DATABASE_URL from a pinned image, so the host needs
# no install and the client is never older than Supabase's server (pg_dump refuses newer servers).
# Usage: DATABASE_URL=... scripts/pg.sh psql [args...]    (stdin is passed through)
# The URL travels as an environment variable, so it never shows in `ps` or in logs.
set -euo pipefail

: "${DATABASE_URL:?set DATABASE_URL (make passes Supabase from .env, or the local Docker database)}"
tool=${1:?usage: scripts/pg.sh psql|pg_dump [args...]}
shift
image=${PG_CLIENT_IMAGE:-postgres:17-alpine}

# Inside the container, localhost is the container itself: point local URLs at the host instead.
url=${DATABASE_URL//@localhost:/@host.docker.internal:}
url=${url//@127.0.0.1:/@host.docker.internal:}

tty=()
[ -t 0 ] && [ -t 1 ] && tty=(-t)
DATABASE_URL=$url exec docker run --rm -i ${tty[@]+"${tty[@]}"} --add-host=host.docker.internal:host-gateway \
  -e DATABASE_URL "$image" sh -c 'tool=$1; shift; exec "$tool" --dbname="$DATABASE_URL" "$@"' sh "$tool" "$@"
