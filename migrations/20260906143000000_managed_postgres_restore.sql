-- filename: 20260906143000000_managed_postgres_restore.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_databases
    ADD COLUMN IF NOT EXISTS restore_source_database_id uuid,
    ADD COLUMN IF NOT EXISTS restore_source_resource_id text,
    ADD COLUMN IF NOT EXISTS restore_point_in_time timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'managed_postgres_restore_source_database_fk') THEN
        ALTER TABLE managed_postgres_databases
            ADD CONSTRAINT managed_postgres_restore_source_database_fk
            FOREIGN KEY (restore_source_database_id)
            REFERENCES managed_postgres_databases(id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'managed_postgres_restore_fields_ck') THEN
        ALTER TABLE managed_postgres_databases
            ADD CONSTRAINT managed_postgres_restore_fields_ck
            CHECK ((restore_source_database_id IS NULL AND restore_source_resource_id IS NULL AND restore_point_in_time IS NULL)
                OR (restore_source_database_id IS NOT NULL AND restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL));
    END IF;
END;
$$;

CREATE INDEX IF NOT EXISTS managed_postgres_databases_restore_source_idx
    ON managed_postgres_databases(restore_source_database_id)
    WHERE restore_source_database_id IS NOT NULL;

CREATE OR REPLACE FUNCTION guard_managed_postgres_restore_sources() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state = 'deleted' AND EXISTS (
        SELECT 1 FROM managed_postgres_databases
        WHERE restore_source_database_id = NEW.id AND state <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'managed postgres database has active restore descendants'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_database_has_restore_descendants';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_restore_sources_guard ON managed_postgres_databases;
CREATE TRIGGER managed_postgres_restore_sources_guard
    BEFORE UPDATE OF state ON managed_postgres_databases
    FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_restore_sources();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS managed_postgres_databases_restore_source_idx;
DROP TRIGGER IF EXISTS managed_postgres_restore_sources_guard ON managed_postgres_databases;
DROP FUNCTION IF EXISTS guard_managed_postgres_restore_sources();
ALTER TABLE managed_postgres_databases
    DROP CONSTRAINT IF EXISTS managed_postgres_restore_fields_ck,
    DROP CONSTRAINT IF EXISTS managed_postgres_restore_source_database_fk,
    DROP COLUMN IF EXISTS restore_point_in_time,
    DROP COLUMN IF EXISTS restore_source_resource_id,
    DROP COLUMN IF EXISTS restore_source_database_id;
-- +goose StatementEnd
