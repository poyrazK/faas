-- +goose Up
ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS environment_clone_operation_id uuid;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_postgres_databases'::regclass AND conname='managed_postgres_clone_has_restore') THEN
        ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_clone_has_restore
            CHECK (environment_clone_operation_id IS NULL OR
                   (restore_source_database_id IS NOT NULL AND restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL));
    END IF;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='managed_postgres_databases'::regclass AND conname='managed_postgres_clone_is_independent') THEN
        ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_clone_is_independent
            CHECK (environment_clone_operation_id IS NULL OR
                   (id <> restore_source_database_id AND (provider_resource_id IS NULL OR provider_resource_id <> restore_source_resource_id)));
    END IF;
END;
$$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS managed_postgres_clone_owner_idx ON managed_postgres_databases(environment_clone_operation_id)
    WHERE environment_clone_operation_id IS NOT NULL;

-- +goose Down
DROP INDEX managed_postgres_clone_owner_idx;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_clone_has_restore;
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_clone_is_independent;
ALTER TABLE managed_postgres_databases DROP COLUMN environment_clone_operation_id;
