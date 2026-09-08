-- 0015_image_kernel.sql
-- Optional direct-kernel boot images (minimal/rescue images): the domain boots
-- kernel+initramfs instead of from a root disk.

ALTER TABLE images
    ADD COLUMN kernel_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN initrd_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN cmdline TEXT NOT NULL DEFAULT '';