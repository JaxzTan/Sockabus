-- Customers, dashboard users, API keys, usage and webhooks (FR-10, FR-12, FR-13; TR-12, TR-13).

-- name: CreateCustomer :one
INSERT INTO customers (name, type) VALUES ($1, $2) RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customers WHERE id = $1;

-- name: SetWebhook :one
UPDATE customers
SET webhook_url = $2, webhook_secret_ref = $3, watch_rules = $4
WHERE id = $1
RETURNING *;

-- name: ListWebhookCustomers :many
SELECT * FROM customers WHERE webhook_url IS NOT NULL ORDER BY id;

-- name: CreateCustomerUser :one
INSERT INTO customer_users (customer_id, email, password_hash) VALUES ($1, $2, $3) RETURNING *;

-- name: GetCustomerUserByEmail :one
SELECT * FROM customer_users WHERE email = $1;

-- name: CreateAPIKey :one
-- Store only the hash; the raw key is shown once (TR-13).
INSERT INTO api_keys (customer_id, key_prefix, key_hash, rate_limit_per_min)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetActiveAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1 AND status = 'active';

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE customer_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIKey :one
UPDATE api_keys SET status = 'revoked', revoked_at = now()
WHERE id = $1 AND customer_id = $2 AND status = 'active'
RETURNING *;

-- name: InsertLookupLog :exec
-- Records that a check happened, never the value checked (NFR-4).
INSERT INTO lookup_logs (customer_id, api_key_id, entity_type, matched, latency_ms, ts)
VALUES ($1, $2, $3, $4, $5, now());

-- name: MonthlyLookupCounts :many
-- Usage page (FR-13).
SELECT date_trunc('month', ts)::timestamptz AS month,
       count(*) AS lookups,
       count(*) FILTER (WHERE matched) AS matches
FROM lookup_logs
WHERE customer_id = $1 AND ts >= sqlc.arg(since)
GROUP BY 1
ORDER BY 1;

-- name: EnqueueWebhookDelivery :one
INSERT INTO webhook_deliveries (customer_id, entity_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListPendingWebhookDeliveries :many
SELECT * FROM webhook_deliveries WHERE status = 'pending' ORDER BY created_at LIMIT $1;

-- name: RecordWebhookAttempt :one
-- One delivery attempt (TR-12). status: delivered on 2xx, failed after the last retry, else stays pending.
UPDATE webhook_deliveries
SET attempts = attempts + 1,
    last_response_code = sqlc.narg(response_code),
    status = sqlc.arg(status),
    delivered_at = CASE WHEN sqlc.arg(status)::delivery_status = 'delivered' THEN now() ELSE delivered_at END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListWebhookDeliveries :many
SELECT * FROM webhook_deliveries WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListCustomers :many
SELECT * FROM customers ORDER BY id;

-- name: CountLookupLogsForKey :one
SELECT count(*) FROM lookup_logs WHERE api_key_id = $1;

-- name: ListLookupLogs :many
-- Usage detail for one customer (FR-13). Never contains the looked-up value.
SELECT * FROM lookup_logs WHERE customer_id = $1 ORDER BY ts DESC, id DESC LIMIT $2;
