-- 0003_cluster.sql
-- Clusters, compute nodes, and node storage pools.

CREATE TABLE clusters (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nodes (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id           UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL UNIQUE,
    agent_endpoint       TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'offline'
                         CHECK (status IN ('healthy', 'degraded', 'offline', 'disabled')),
    cpu_capacity         INTEGER NOT NULL DEFAULT 0,
    memory_capacity_mb   BIGINT NOT NULL DEFAULT 0,
    storage_capacity_gb  BIGINT NOT NULL DEFAULT 0,
    cpu_usage_percent    REAL NOT NULL DEFAULT 0,
    memory_usage_percent REAL NOT NULL DEFAULT 0,
    last_heartbeat       TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX nodes_cluster_idx ON nodes (cluster_id, status);

CREATE TABLE storage_pools (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id    UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    path       TEXT NOT NULL DEFAULT '',
    type       TEXT NOT NULL DEFAULT 'dir',
    total_bytes BIGINT NOT NULL DEFAULT 0,
    used_bytes  BIGINT NOT NULL DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, name)
);