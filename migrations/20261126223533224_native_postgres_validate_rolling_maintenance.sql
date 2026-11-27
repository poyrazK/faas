-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE native_postgres_base_refreshes
    VALIDATE CONSTRAINT native_postgres_base_refreshes_rolling_check;
