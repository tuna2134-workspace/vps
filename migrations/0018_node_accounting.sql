-- 0018_node_accounting.sql
-- CPU overcommit accounting and storage-free-capacity scheduling.
--
-- nodes gains the physical core count (set by heartbeats), a per-node CPU
-- overcommit ratio, and the reported free storage bytes. Scheduling decisions
-- use allocated_vcpu (SUM of VM vcpu) instead of instantaneous CPU usage.

ALTER TABLE nodes
    ADD COLUMN physical_cpu_cores  INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN cpu_overcommit_ratio NUMERIC NOT NULL DEFAULT 1.0,
    ADD COLUMN storage_free_bytes   BIGINT  NOT NULL DEFAULT 0;

UPDATE nodes SET physical_cpu_cores = cpu_capacity WHERE physical_cpu_cores = 0;

-- Resource reservations make capacity accounting concurrency-safe: a node row
-- is locked, capacity is checked against existing VMs plus active
-- reservations, and a reservation is inserted before the VM record is
-- committed. The reservation is released on provisioning failure and removed
-- on success.
CREATE TABLE resource_reservations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id       UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    vm_id         UUID NOT NULL UNIQUE,
    cpu_vcpu      INTEGER NOT NULL DEFAULT 0,
    memory_bytes  BIGINT  NOT NULL DEFAULT 0,
    storage_bytes BIGINT  NOT NULL DEFAULT 0,
    state         TEXT NOT NULL DEFAULT 'pending'
                  CHECK (state IN ('pending', 'committed', 'released')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX resource_reservations_node_state_idx ON resource_reservations (node_id, state);