-- filename: 20261121140659115_retire_base_image_producers_environment_keys.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE base_image_producers DROP CONSTRAINT IF EXISTS base_image_producers_storage_key_check;
-- +goose Down
-- Retain immutable publication evidence through reader rollback.
SELECT 1;
