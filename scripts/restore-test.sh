#!/usr/bin/env bash
# Proves a dump restores (TR-23): loads it into a throwaway PostgreSQL container, counts rows in key
# tables, then removes the container. Supabase and the local database are never touched.
# Usage: scripts/restore-test.sh [backups/buru-<time>.sql.gz]   (default: newest dump)
set -euo pipefail

file=${1:-$(ls -t backups/buru-*.sql.gz 2>/dev/null | head -1)}
[ -n "$file" ] && [ -f "$file" ] || { echo "no dump found in backups/ (run make backup)" >&2; exit 1; }
image=${PG_CLIENT_IMAGE:-postgres:17-alpine}
name=buru-restore-test-$$

docker run -d --rm --name "$name" -e POSTGRES_PASSWORD=restore-test -e POSTGRES_DB=buru "$image" > /dev/null
trap 'docker rm -f "$name" > /dev/null 2>&1' EXIT

# -h 127.0.0.1: the image's first-boot server listens on the socket only, so this waits for the real one.
for _ in $(seq 1 60); do
  docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d buru && break
  sleep 1
done
psql() { docker exec -i "$name" psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U postgres -d buru "$@"; }

# The dump is --schema=public, which recreates the schema itself.
psql -qc "DROP SCHEMA public CASCADE"
gunzip -c "$file" | psql -q > /dev/null
echo "restored $file into a scratch container:"
psql -c "SELECT
  (SELECT count(*) FROM personas)      AS personas,
  (SELECT count(*) FROM conversations) AS conversations,
  (SELECT count(*) FROM messages)      AS messages,
  (SELECT count(*) FROM entities)      AS entities"
