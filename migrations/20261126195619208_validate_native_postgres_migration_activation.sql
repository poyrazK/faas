-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE release_database_switches
    VALIDATE CONSTRAINT release_database_switches_migration_check;

-- +goose Down
SELECT 1;
