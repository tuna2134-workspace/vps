-- 0009_billing.sql
-- Stripe billing entities. stripe_event_id / stripe_*_id uniqueness makes
-- webhook processing idempotent at the database level.

CREATE TABLE billing_customers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    stripe_customer_id  TEXT NOT NULL UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vm_id                   UUID REFERENCES vms(id) ON DELETE SET NULL,
    plan_id                 UUID REFERENCES plans(id),
    stripe_subscription_id  TEXT NOT NULL UNIQUE,
    status                  TEXT NOT NULL DEFAULT 'incomplete'
                            CHECK (status IN ('incomplete', 'active', 'trialing', 'past_due',
                                              'canceled', 'unpaid', 'incomplete_expired')),
    billing_status          TEXT NOT NULL DEFAULT 'unpaid'
                            CHECK (billing_status IN ('active', 'past_due', 'unpaid', 'canceled', 'suspended')),
    current_period_start    TIMESTAMPTZ,
    current_period_end      TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX subscriptions_user_idx ON subscriptions (user_id);
CREATE INDEX subscriptions_status_idx ON subscriptions (billing_status);

CREATE TABLE invoices (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_invoice_id    TEXT NOT NULL UNIQUE,
    amount_cents         INTEGER NOT NULL,
    currency             CHAR(3) NOT NULL DEFAULT 'USD',
    status               TEXT NOT NULL DEFAULT 'open',
    paid_at              TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX invoices_user_idx ON invoices (user_id, created_at);

CREATE TABLE payments (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stripe_payment_intent_id    TEXT NOT NULL UNIQUE,
    amount_cents                INTEGER NOT NULL,
    currency                    CHAR(3) NOT NULL DEFAULT 'USD',
    status                      TEXT NOT NULL DEFAULT 'requires_payment_method',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX payments_user_idx ON payments (user_id, created_at);

-- Tracks processed Stripe webhook events for idempotent processing.
CREATE TABLE webhook_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stripe_event_id TEXT NOT NULL UNIQUE,
    event_type     TEXT NOT NULL,
    processed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);