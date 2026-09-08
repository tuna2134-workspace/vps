-- 0005_networks.sql
-- Networks, IP pools (IPAM), and IP allocations.

CREATE TABLE networks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    bridge      TEXT NOT NULL,
    ipv4_cidr   TEXT NOT NULL DEFAULT '',
    ipv6_cidr   TEXT NOT NULL DEFAULT '',
    dns1        TEXT NOT NULL DEFAULT '1.1.1.1',
    dns2        TEXT NOT NULL DEFAULT '1.0.0.1',
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ip_pools (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network_id UUID NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    cidr       TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('ipv4', 'ipv6')),
    gateway    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (network_id, cidr, type)
);

CREATE TABLE ip_allocations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pool_id      UUID NOT NULL REFERENCES ip_pools(id) ON DELETE CASCADE,
    vm_id        UUID,
    ip_address   TEXT NOT NULL,
    mac_address  TEXT NOT NULL,
    gateway      TEXT NOT NULL DEFAULT '',
    prefix       INTEGER NOT NULL,
    status       TEXT NOT NULL DEFAULT 'allocated'
                 CHECK (status IN ('allocated', 'released')),
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at  TIMESTAMPTZ
);

-- Enforce uniqueness of live allocations; released rows can be reused later.
CREATE UNIQUE INDEX ip_allocations_live_unique_idx
    ON ip_allocations (pool_id, ip_address) WHERE status = 'allocated';
CREATE UNIQUE INDEX ip_allocations_vm_unique_idx
    ON ip_allocations (vm_id, status) WHERE status = 'allocated';

CREATE INDEX ip_allocations_pool_status_idx ON ip_allocations (pool_id, status);