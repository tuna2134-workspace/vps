-- 0011_audit.sql
-- Security-relevant event log. Never store passwords, tokens, keys, or
-- PII in the metadata column.

CREATE TABLE audit_logs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id   TEXT NOT NULL DEFAULT '',
    ip            INET,
    user_agent    TEXT NOT NULL DEFAULT '',
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_user_idx ON audit_logs (user_id, created_at);
CREATE INDEX audit_logs_action_idx ON audit_logs (action, created_at);
CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id);