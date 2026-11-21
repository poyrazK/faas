-- filename: 20261121140659120_validate_base_image_scans_environment_keys.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE base_image_scans VALIDATE CONSTRAINT base_image_scans_run_environment_storage_key_check;
-- +goose Down
-- Retain immutable publication evidence through reader rollback.
SELECT 1;
