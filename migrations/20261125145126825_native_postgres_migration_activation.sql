-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE release_database_switches
    ALTER COLUMN restore_point DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS migration_operation_id uuid,
    ADD CONSTRAINT release_database_switches_migration_check
        CHECK ((restore_point IS NOT NULL) = (migration_operation_id IS NULL)) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE release_database_switches
    DROP CONSTRAINT release_database_switches_migration_check;
-- An activation has no restore point. This downgrade requires an empty activation history.
ALTER TABLE release_database_switches
    ALTER COLUMN restore_point SET NOT NULL,
    DROP COLUMN migration_operation_id;
