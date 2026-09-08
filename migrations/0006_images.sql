-- 0006_images.sql
-- Base OS images that can be cloned to provision VMs.

CREATE TABLE images (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,
    version             TEXT NOT NULL,
    architecture        TEXT NOT NULL DEFAULT 'x86_64',
    format              TEXT NOT NULL DEFAULT 'qcow2'
                        CHECK (format IN ('qcow2', 'raw', 'iso')),
    source_url          TEXT NOT NULL,
    checksum            TEXT NOT NULL DEFAULT '',
    size_bytes          BIGINT NOT NULL DEFAULT 0,
    cloud_init_compatible BOOLEAN NOT NULL DEFAULT false,
    status              TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'disabled')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);