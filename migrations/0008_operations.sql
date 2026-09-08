-- 0008_operations.sql
-- Async VM operations. Idempotency keys make retries safe: the same request
-- (identified by the same idempotency key) can never create two operations.

CREATE TABLE vm_operations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id           UUID REFERENCES vms(id) ON DELETE SET NULL,
    operation_type  TEXT NOT NULL
                    CHECK (operation_type IN ('create', 'delete', 'start', 'stop',
                                              'force_stop', 'reboot', 'terminate')),
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    idempotency_key TEXT NOT NULL UNIQUE,
    error           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);

CREATE INDEX vm_operations_vm_idx ON vm_operations (vm_id, created_at);
CREATE INDEX vm_operations_status_idx ON vm_operations (status) WHERE status IN ('pending', 'running');