-- filename: 20261121140659119_expand_base_image_scans_environment_keys.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'base_image_scans'::regclass AND conname = 'base_image_scans_run_environment_storage_key_check') THEN
  ALTER TABLE base_image_scans ADD CONSTRAINT base_image_scans_run_environment_storage_key_check CHECK ((storage_key ~ '^base/[^/]+[.]ext4$' OR storage_key ~ '^run-environments/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{32})/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/base[.]ext4$') AND length(storage_key)<=512) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Retain immutable publication evidence through reader rollback.
SELECT 1;
