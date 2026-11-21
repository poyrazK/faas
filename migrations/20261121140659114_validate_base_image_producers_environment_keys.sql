-- filename: 20261121140659114_validate_base_image_producers_environment_keys.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE base_image_producers VALIDATE CONSTRAINT base_image_producers_run_environment_storage_key_check;
-- +goose Down
-- Retain immutable publication evidence through reader rollback.
SELECT 1;
