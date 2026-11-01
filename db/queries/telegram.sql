-- Telegram update state (G-9) and account health events (G-10), migration 000010.

-- name: GetTelegramUpdateState :one
SELECT * FROM telegram_update_state WHERE account_id = $1;

-- name: SetTelegramUpdateState :exec
INSERT INTO telegram_update_state (account_id, pts, qts, date, seq, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (account_id) DO UPDATE
SET pts = EXCLUDED.pts, qts = EXCLUDED.qts, date = EXCLUDED.date, seq = EXCLUDED.seq, updated_at = now();

-- name: SetTelegramPts :exec
INSERT INTO telegram_update_state (account_id, pts) VALUES ($1, $2)
ON CONFLICT (account_id) DO UPDATE SET pts = EXCLUDED.pts, updated_at = now();

-- name: SetTelegramQts :exec
INSERT INTO telegram_update_state (account_id, qts) VALUES ($1, $2)
ON CONFLICT (account_id) DO UPDATE SET qts = EXCLUDED.qts, updated_at = now();

-- name: SetTelegramDate :exec
INSERT INTO telegram_update_state (account_id, date) VALUES ($1, $2)
ON CONFLICT (account_id) DO UPDATE SET date = EXCLUDED.date, updated_at = now();

-- name: SetTelegramSeq :exec
INSERT INTO telegram_update_state (account_id, seq) VALUES ($1, $2)
ON CONFLICT (account_id) DO UPDATE SET seq = EXCLUDED.seq, updated_at = now();

-- name: SetTelegramDateSeq :exec
INSERT INTO telegram_update_state (account_id, date, seq) VALUES ($1, $2, $3)
ON CONFLICT (account_id) DO UPDATE SET date = EXCLUDED.date, seq = EXCLUDED.seq, updated_at = now();

-- name: InsertAccountHealthEvent :one
INSERT INTO account_health_events (account_id, event, detail) VALUES ($1, $2, $3) RETURNING *;

-- name: ListAccountHealthEvents :many
SELECT * FROM account_health_events WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2;
