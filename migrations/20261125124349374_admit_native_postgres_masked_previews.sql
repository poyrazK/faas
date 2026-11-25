-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_databases_previews_chk;

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- Rollback requires masked preview settings to be disabled first.
ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_databases_previews_chk
    CHECK (previews IN ('off', 'empty', 'copy'));
