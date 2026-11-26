-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS native_postgres_masked_branches (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    source_resource_id text NOT NULL,
    policy jsonb NOT NULL CHECK (jsonb_typeof(policy) = 'array'),
    validated_at timestamptz
);

-- +goose StatementBegin
DO $create_function$
BEGIN
    IF to_regprocedure('guard_native_postgres_masked_branch_ready()') IS NULL THEN
        CREATE FUNCTION guard_native_postgres_masked_branch_ready() RETURNS trigger
        LANGUAGE plpgsql AS $$
        BEGIN
            -- Logical branches have a source database but no physical restore point.
            IF NEW.state = 'ready' AND NEW.restore_source_database_id IS NOT NULL
                AND NEW.restore_source_resource_id IS NULL AND NEW.restore_point_in_time IS NULL
                AND NOT EXISTS (SELECT FROM native_postgres_masked_branches b
                    WHERE b.database_id = NEW.id AND b.validated_at IS NOT NULL) THEN
                RAISE EXCEPTION 'masked branch validation is incomplete' USING ERRCODE = '23514';
            END IF;
            RETURN NEW;
        END;
        $$;
    END IF;
END;
$create_function$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'managed_postgres_databases'::regclass
        AND tgname = 'native_postgres_masked_branch_ready') THEN
        CREATE TRIGGER native_postgres_masked_branch_ready
            BEFORE INSERT OR UPDATE OF state ON managed_postgres_databases
            FOR EACH ROW EXECUTE FUNCTION guard_native_postgres_masked_branch_ready();
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_postgres_databases'::regclass
        AND conname = 'managed_postgres_copy_fields_ck') THEN
        ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_copy_fields_ck CHECK (
        (restore_source_resource_id IS NULL AND restore_point_in_time IS NULL)
        OR (restore_source_database_id IS NOT NULL AND restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL)
    ) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_copy_fields_ck;
DROP TRIGGER native_postgres_masked_branch_ready ON managed_postgres_databases;
DROP FUNCTION guard_native_postgres_masked_branch_ready();
DROP TABLE native_postgres_masked_branches;
