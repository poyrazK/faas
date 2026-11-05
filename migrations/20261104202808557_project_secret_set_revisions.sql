-- filename: 20261104202808557_project_secret_set_revisions.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep a revision even when the final secret is deleted. N-1 writers also
-- advance it through these triggers; a master-key reseal does not.
ALTER TABLE project_environments ADD COLUMN IF NOT EXISTS shared_secret_generation bigint NOT NULL DEFAULT 0;

-- Preserve the validated constraint when the schema is ahead of the ledger.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
        WHERE conrelid = 'project_environments'::regclass
          AND conname = 'project_environments_shared_secret_generation_check') THEN
        ALTER TABLE project_environments ADD CONSTRAINT project_environments_shared_secret_generation_check
            CHECK (shared_secret_generation >= 0) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION advance_project_shared_secret_generation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p uuid; environment text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        p := OLD.project_id; environment := OLD.environment_slug;
    ELSE
        p := NEW.project_id; environment := NEW.environment_slug;
    END IF;
    UPDATE project_environments SET shared_secret_generation = shared_secret_generation + 1
    WHERE project_id = p AND slug = environment;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER project_shared_secret_set_membership
    AFTER INSERT OR DELETE ON project_environment_secrets
    FOR EACH ROW EXECUTE FUNCTION advance_project_shared_secret_generation();
CREATE OR REPLACE TRIGGER project_shared_secret_set_value
    AFTER UPDATE ON project_environment_secrets
    FOR EACH ROW WHEN (OLD.secret_version IS DISTINCT FROM NEW.secret_version OR OLD.value_hash IS DISTINCT FROM NEW.value_hash)
    EXECUTE FUNCTION advance_project_shared_secret_generation();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER project_shared_secret_set_value ON project_environment_secrets;
DROP TRIGGER project_shared_secret_set_membership ON project_environment_secrets;
DROP FUNCTION advance_project_shared_secret_generation();
ALTER TABLE project_environments DROP COLUMN shared_secret_generation;
