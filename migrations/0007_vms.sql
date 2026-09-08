-- 0007_vms.sql
-- Virtual machines. Each VM carries its own resource allocation snapshot so a
-- later plan change does not alter already-provisioned instances.

CREATE TABLE vms (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id       UUID REFERENCES plans(id),
    node_id       UUID REFERENCES nodes(id),
    network_id    UUID REFERENCES networks(id),
    image_id      UUID REFERENCES images(id),
    name          TEXT NOT NULL,
    hostname      TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'provisioning', 'running', 'stopped',
                                    'suspended', 'terminated', 'error')),
    vcpu          INTEGER NOT NULL,
    memory_mb     INTEGER NOT NULL,
    disk_gb       INTEGER NOT NULL,
    mac_address   TEXT NOT NULL UNIQUE,
    instance_id   TEXT NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX vms_user_idx ON vms (user_id);
CREATE INDEX vms_node_idx ON vms (node_id, status) WHERE deleted_at IS NULL;
CREATE INDEX vms_status_idx ON vms (status) WHERE deleted_at IS NULL;