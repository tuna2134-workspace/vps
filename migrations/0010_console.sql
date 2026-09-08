-- 0010_console.sql
-- Short-lived, single-use console access tokens. Tokens are stored hashed.

CREATE TABLE console_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id        UUID NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    console_type TEXT NOT NULL DEFAULT 'vnc' CHECK (console_type IN ('vnc', 'serial')),
    host         TEXT NOT NULL,
    port         INTEGER NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX console_tokens_vm_idx ON console_tokens (vm_id, created_at);