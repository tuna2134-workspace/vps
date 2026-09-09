-- 0019_ipv6_prefix.sql
-- IPv6 prefix delegation IPAM. IPv6 pools can allocate a /64 (or other)
-- delegated prefix from a large parent (e.g. /48) by index instead of scanning
-- addresses one by one.

ALTER TABLE ip_pools
    ADD COLUMN allocation_type           TEXT NOT NULL DEFAULT 'address'
                CHECK (allocation_type IN ('address', 'prefix')),
    ADD COLUMN delegation_prefix_length  INTEGER;

ALTER TABLE ip_allocations
    ADD COLUMN allocation_index BIGINT;

-- A delegated prefix is identified by (pool, index); an index is never
-- reused while allocated.
CREATE UNIQUE INDEX ip_allocations_prefix_index_unique
    ON ip_allocations (pool_id, allocation_index)
    WHERE allocation_index IS NOT NULL AND status = 'allocated';