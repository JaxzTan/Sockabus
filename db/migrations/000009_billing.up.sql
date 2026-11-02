-- Billing (FR-13 "usage + billing", TRD §4 lookup_logs note): plans, one subscription per customer
-- period, and invoices built from lookup_logs counts. Amounts in MYR by default.
-- Additive: new tables and indexes only; no plans or prices are inserted here.

CREATE TABLE plans (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code                  varchar(32)    NOT NULL UNIQUE, -- e.g. starter, growth, enterprise
    name                  varchar(100)   NOT NULL,
    monthly_price         numeric(12, 2) NOT NULL CHECK (monthly_price >= 0),
    currency              char(3)        NOT NULL DEFAULT 'MYR',
    included_lookups      int            NOT NULL DEFAULT 0 CHECK (included_lookups >= 0),
    overage_price         numeric(12, 4) NOT NULL DEFAULT 0 CHECK (overage_price >= 0), -- per lookup above included_lookups
    active                boolean        NOT NULL DEFAULT true,
    created_at            timestamptz    NOT NULL DEFAULT now()
);

CREATE TABLE customer_subscriptions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id  bigint      NOT NULL REFERENCES customers (id),
    plan_id      bigint      NOT NULL REFERENCES plans (id),
    period_start timestamptz NOT NULL,
    period_end   timestamptz NOT NULL,
    status       varchar(16) NOT NULL DEFAULT 'active'
        CHECK (status IN ('trialing', 'active', 'past_due', 'cancelled', 'expired')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    cancelled_at timestamptz,
    CHECK (period_end > period_start)
);
CREATE INDEX customer_subscriptions_customer_idx ON customer_subscriptions (customer_id, period_start DESC);
-- At most one live subscription per customer.
CREATE UNIQUE INDEX customer_subscriptions_live_uq ON customer_subscriptions (customer_id)
    WHERE status IN ('trialing', 'active', 'past_due');

CREATE TABLE invoices (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id      bigint         NOT NULL REFERENCES customers (id),
    subscription_id  bigint         REFERENCES customer_subscriptions (id),
    period_start     timestamptz    NOT NULL,
    period_end       timestamptz    NOT NULL,
    lookup_count     int            NOT NULL DEFAULT 0 CHECK (lookup_count >= 0), -- from lookup_logs
    included_lookups int            NOT NULL DEFAULT 0,
    overage_lookups  int            NOT NULL DEFAULT 0,
    amount           numeric(12, 2) NOT NULL DEFAULT 0 CHECK (amount >= 0),
    currency         char(3)        NOT NULL DEFAULT 'MYR',
    status           varchar(16)    NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'issued', 'paid', 'void')),
    issued_at        timestamptz,
    paid_at          timestamptz,
    created_at       timestamptz    NOT NULL DEFAULT now(),
    CHECK (period_end > period_start),
    UNIQUE (customer_id, period_start)
);
CREATE INDEX invoices_status_idx ON invoices (status, period_end);

DO $$
DECLARE
    t text;
    r text;
BEGIN
    FOREACH t IN ARRAY ARRAY['plans', 'customer_subscriptions', 'invoices'] LOOP
        IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'buru_app') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %I TO buru_app', t);
        END IF;
        FOREACH r IN ARRAY ARRAY['anon', 'authenticated'] LOOP
            IF EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
                EXECUTE format('REVOKE ALL ON %I FROM %I', t, r);
            END IF;
        END LOOP;
    END LOOP;
END
$$;
