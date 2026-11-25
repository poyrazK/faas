-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_masked_branches (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    source_resource_id text NOT NULL,
    policy jsonb NOT NULL CHECK (jsonb_typeof(policy) = 'array'),
    validated_at timestamptz
);

-- +goose StatementBegin
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
-- +goose StatementEnd

CREATE TRIGGER native_postgres_masked_branch_ready
    BEFORE INSERT OR UPDATE OF state ON managed_postgres_databases
    FOR EACH ROW EXECUTE FUNCTION guard_native_postgres_masked_branch_ready();

ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_copy_fields_ck CHECK (
    (restore_source_resource_id IS NULL AND restore_point_in_time IS NULL)
    OR (restore_source_database_id IS NOT NULL AND restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL)
) NOT VALID;

-- +goose Down
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_copy_fields_ck;
DROP TRIGGER native_postgres_masked_branch_ready ON managed_postgres_databases;
DROP FUNCTION guard_native_postgres_masked_branch_ready();
DROP TABLE native_postgres_masked_branches;
