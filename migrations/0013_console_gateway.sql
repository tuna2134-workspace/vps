-- 0013_console_gateway.sql
-- Serial consoles are accessed through the agent's streaming Console RPC
-- (virDomainOpenConsole) rather than a raw PTY path, so the gateway needs the
-- node endpoint and domain name to reach the agent.

ALTER TABLE console_tokens
    ADD COLUMN node_endpoint TEXT NOT NULL DEFAULT '',
    ADD COLUMN vm_name TEXT NOT NULL DEFAULT '';