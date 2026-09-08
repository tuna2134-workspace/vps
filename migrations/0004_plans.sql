-- 0004_plans.sql
-- VPS plans with an append-only version history. Existing subscriptions keep
-- their plan snapshot; plan changes never mutate live subscriptions.

CREATE TABLE plans (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT NOT NULL UNIQUE,
    description        TEXT NOT NULL DEFAULT '',
    vcpu               INTEGER NOT NULL CHECK (vcpu > 0),
    memory_mb          INTEGER NOT NULL CHECK (memory_mb > 0),
    disk_gb            INTEGER NOT NULL CHECK (disk_gb > 0),
    bandwidth_gb       INTEGER NOT NULL DEFAULT 0,
    network_speed_mbps INTEGER NOT NULL DEFAULT 0,
    ipv4_count         INTEGER NOT NULL DEFAULT 1 CHECK (ipv4_count >= 0),
    ipv6_prefix        INTEGER NOT NULL DEFAULT 0,
    monthly_price_cents INTEGER NOT NULL DEFAULT 0 CHECK (monthly_price_cents >= 0),
    currency           CHAR(3) NOT NULL DEFAULT 'USD',
    version            INTEGER NOT NULL DEFAULT 1,
    active             BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Historical snapshots of a plan.
CREATE TABLE plan_versions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id            UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    version            INTEGER NOT NULL,
    name               TEXT NOT NULL,
    vcpu               INTEGER NOT NULL,
    memory_mb          INTEGER NOT NULL,
    disk_gb            INTEGER NOT NULL,
    bandwidth_gb       INTEGER NOT NULL DEFAULT 0,
    network_speed_mbps INTEGER NOT NULL DEFAULT 0,
    ipv4_count         INTEGER NOT NULL DEFAULT 1,
    ipv6_prefix        INTEGER NOT NULL DEFAULT 0,
    monthly_price_cents INTEGER NOT NULL DEFAULT 0,
    currency           CHAR(3) NOT NULL DEFAULT 'USD',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (plan_id, version)
);