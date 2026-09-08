-- 0002_sessions.sql
-- Opaque session tokens are stored hashed (SHA-256); the raw token is never
-- persisted or logged.

CREATE TABLE sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip          INET NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT '',
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX sessions_user_idx ON sessions (user_id, revoked_at);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);