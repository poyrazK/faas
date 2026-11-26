-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT managed_postgres_previews_mode_ck;

-- +goose Down
SELECT 1;
