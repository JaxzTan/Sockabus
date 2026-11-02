#!/usr/bin/env bash
# Nightly PostgreSQL dump (TR-23) of the Supabase database in DATABASE_URL (the Makefile exports .env).
# Writes backups/buru-<UTC time>.sql.gz, keeps 14 days locally, and copies it off the server when
# BACKUP_REMOTE is set (an rclone destination, e.g. r2:buru-backups). On success it GETs
# SOCKABUS_KUMA_BACKUP_PUSH_URL when set, so an Uptime Kuma push monitor alerts if a night is missed.
# Cron on the VPS: 0 3 * * * cd /opt/buru && make backup >> backups/backup.log 2>&1
set -euo pipefail

: "${DATABASE_URL:?set DATABASE_URL in .env (Supabase owner, session pooler port 5432)}"

dir=backups
mkdir -p "$dir"
rm -f "$dir"/*.part
file="$dir/buru-$(date -u +%Y%m%dT%H%M%SZ).sql.gz"

# Only the public schema: Supabase's own schemas (auth, storage, ...) are not ours to restore.
# Written to .part first, so a failed dump never becomes the "newest backup" restore-test picks.
# scripts/pg.sh runs pg_dump 17 from an image: never older than the server, nothing to install.
"$(dirname "$0")/pg.sh" pg_dump --schema=public --no-owner --no-privileges < /dev/null | gzip > "$file.part"
mv "$file.part" "$file"
echo "wrote $file ($(du -h "$file" | cut -f1))"

if [ -n "${BACKUP_REMOTE:-}" ]; then
  rclone copy "$file" "$BACKUP_REMOTE"
  echo "copied to $BACKUP_REMOTE"
else
  echo "BACKUP_REMOTE not set: dump is on this server only" >&2
fi

find "$dir" -name 'buru-*.sql.gz' -mtime +14 -delete

if [ -n "${SOCKABUS_KUMA_BACKUP_PUSH_URL:-}" ]; then
  curl -fsS -m 10 -o /dev/null "$SOCKABUS_KUMA_BACKUP_PUSH_URL" || echo "Uptime Kuma backup push failed" >&2
fi
