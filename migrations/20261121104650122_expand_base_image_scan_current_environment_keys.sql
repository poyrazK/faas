-- filename: 20261121104650122_expand_base_image_scan_current_environment_keys.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE base_image_scan_current ADD CONSTRAINT base_image_scan_current_run_environment_storage_key_check CHECK ((storage_key ~ '^base/[^/]+[.]ext4$' OR storage_key ~ '^run-environments/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{32})/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/base[.]ext4$') AND length(storage_key)<=512) NOT VALID;
-- +goose Down
-- Retain immutable publication evidence through reader rollback.
SELECT 1;
