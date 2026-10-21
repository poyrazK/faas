-- filename: 20261021083436183_project_environment_secrets_follow_environment.sql

-- ADR-1139: an environment's secrets leave with it on every deletion path
-- (environment delete, clone cleanup), so a later environment of the same slug
-- never inherits them. Separate from the table's migration to keep each
-- transaction's locks on existing tables within migration-lint's bound.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_project_environment_secrets()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM project_environment_secrets
     WHERE project_id = OLD.project_id AND environment_slug = OLD.slug;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER project_environment_secrets_follow_environment
    AFTER DELETE ON project_environments
    FOR EACH ROW EXECUTE FUNCTION delete_project_environment_secrets();

-- +goose Down
DROP TRIGGER IF EXISTS project_environment_secrets_follow_environment ON project_environments;
DROP FUNCTION IF EXISTS delete_project_environment_secrets();
