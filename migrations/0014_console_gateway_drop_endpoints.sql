-- 0014_console_gateway_drop_endpoints.sql
-- Both serial and VNC consoles are now accessed through the agent's streaming
-- Console RPC (virDomainOpenConsole / virDomainOpenGraphicsFD). The token no
-- longer needs host/port/path endpoints.

ALTER TABLE console_tokens
    DROP COLUMN host,
    DROP COLUMN port,
    DROP COLUMN path;