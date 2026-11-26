-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_databases DROP CONSTRAINT IF EXISTS managed_postgres_restore_fields_ck;

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- Rollback requires an empty logical branch history.
ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_restore_fields_ck CHECK (
    (restore_source_database_id IS NULL AND restore_source_resource_id IS NULL AND restore_point_in_time IS NULL)
    OR (restore_source_database_id IS NOT NULL AND restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL)
);
