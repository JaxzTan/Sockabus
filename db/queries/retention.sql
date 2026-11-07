-- Retention purge (TRD §7, migration 000013). Run by cmd/retention-purge as the schema owner or a member of
-- buru_purge; buru_app cannot execute it.

-- name: PurgeExpired :one
SELECT id, retention, cutoff, dry_run, messages_deleted, sightings_deleted, summaries_deleted, requested_by, db_user, created_at
FROM buru_purge_expired(sqlc.arg(retention)::interval, sqlc.arg(dry_run)::boolean, sqlc.arg(requested_by)::text);

-- name: ListRetentionPurges :many
SELECT * FROM retention_purges ORDER BY created_at DESC, id DESC LIMIT $1;
