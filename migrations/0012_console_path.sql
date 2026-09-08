-- 0012_console_path.sql
-- Add serial console PTY path support to console tokens. Serial consoles do
-- not use host/port, so make those columns default to empty values.

ALTER TABLE console_tokens
    ADD COLUMN path TEXT NOT NULL DEFAULT '';

ALTER TABLE console_tokens
    ALTER COLUMN host SET DEFAULT '',
    ALTER COLUMN port SET DEFAULT 0;