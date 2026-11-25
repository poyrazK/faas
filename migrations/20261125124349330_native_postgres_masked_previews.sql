-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_preview_masking_policies (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    policy jsonb NOT NULL CHECK (jsonb_typeof(policy) = 'array'),
    updated_at timestamptz NOT NULL
);

ALTER TABLE native_postgres_masked_branches ADD COLUMN preview_policy_revision bigint;

ALTER TABLE managed_postgres_databases ADD CONSTRAINT managed_postgres_previews_mode_ck
    CHECK (previews IN ('off', 'empty', 'copy', 'masked')) NOT VALID;

-- +goose StatementBegin
CREATE FUNCTION guard_native_postgres_masked_preview_owner() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT FROM managed_postgres_databases s
        WHERE s.id = NEW.preview_source_database_id AND s.previews = 'masked')
        AND (NEW.restore_source_database_id IS DISTINCT FROM NEW.preview_source_database_id
            OR NEW.restore_source_resource_id IS NOT NULL OR NEW.restore_point_in_time IS NOT NULL) THEN
        RAISE EXCEPTION 'masked preview requires a logical copy' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER native_postgres_masked_preview_owner
    BEFORE INSERT OR UPDATE OF preview_source_database_id ON managed_postgres_databases
    FOR EACH ROW WHEN (NEW.preview_source_database_id IS NOT NULL)
    EXECUTE FUNCTION guard_native_postgres_masked_preview_owner();

-- +goose Down
DROP TRIGGER native_postgres_masked_preview_owner ON managed_postgres_databases;
DROP FUNCTION guard_native_postgres_masked_preview_owner();
ALTER TABLE managed_postgres_databases DROP CONSTRAINT managed_postgres_previews_mode_ck;
ALTER TABLE native_postgres_masked_branches DROP COLUMN preview_policy_revision;
DROP TABLE native_postgres_preview_masking_policies;
