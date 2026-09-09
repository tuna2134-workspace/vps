-- 0017_vm_access.sql
-- Persist the access credentials a user supplies at VM creation so the async
-- provisioner can apply them via cloud-init. Previously ssh_keys and
-- root_password were accepted by the API but silently dropped, leaving
-- VMs unloginable. root_password is stored plaintext because the provisioner
-- needs the exact value to seed the OS; it is never returned by the API.

ALTER TABLE vms ADD COLUMN ssh_keys      JSONB NOT NULL DEFAULT '[]';
ALTER TABLE vms ADD COLUMN root_password TEXT  NOT NULL DEFAULT '';