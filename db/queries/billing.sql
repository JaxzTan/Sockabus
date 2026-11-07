-- Billing (FR-13), migration 000009: plans, customer subscriptions, invoices from lookup_logs.

-- name: CreatePlan :one
INSERT INTO plans (code, name, monthly_price, currency, included_lookups, overage_price)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPlanByCode :one
SELECT * FROM plans WHERE code = $1;

-- name: ListActivePlans :many
SELECT * FROM plans WHERE active ORDER BY monthly_price, id;

-- name: CreateSubscription :one
INSERT INTO customer_subscriptions (customer_id, plan_id, period_start, period_end, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: LiveSubscription :one
-- The customer's current subscription (trialing, active or past_due), if any.
SELECT * FROM customer_subscriptions
WHERE customer_id = $1 AND status IN ('trialing', 'active', 'past_due');

-- name: SetSubscriptionStatus :one
UPDATE customer_subscriptions
SET status = sqlc.arg(status),
    cancelled_at = CASE WHEN sqlc.arg(status)::text = 'cancelled' THEN now() ELSE cancelled_at END
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountLookupsInPeriod :one
SELECT count(*) FROM lookup_logs WHERE customer_id = $1 AND ts >= sqlc.arg(period_start) AND ts < sqlc.arg(period_end);

-- name: UpsertDraftInvoice :one
-- (Re)computes a draft invoice for one period. Issued, paid and void invoices are never changed.
INSERT INTO invoices (customer_id, subscription_id, period_start, period_end, lookup_count, included_lookups, overage_lookups, amount, currency)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (customer_id, period_start) DO UPDATE
SET lookup_count = EXCLUDED.lookup_count, included_lookups = EXCLUDED.included_lookups,
    overage_lookups = EXCLUDED.overage_lookups, amount = EXCLUDED.amount, period_end = EXCLUDED.period_end
WHERE invoices.status = 'draft'
RETURNING *;

-- name: IssueInvoice :one
UPDATE invoices SET status = 'issued', issued_at = now() WHERE id = $1 AND status = 'draft' RETURNING *;

-- name: MarkInvoicePaid :one
UPDATE invoices SET status = 'paid', paid_at = now() WHERE id = $1 AND status = 'issued' RETURNING *;

-- name: ListInvoices :many
SELECT * FROM invoices WHERE customer_id = $1 ORDER BY period_start DESC LIMIT $2;
