-- filename: 20261018084222080_project_config_runtime_env.sql

-- ADR-1136: a project environment's configuration reaches its services as
-- environment variables. A new version that changes the delivered variables
-- stamps a runtime-configuration change for every member app in the affected
-- scope and marks those snapshots stale, as an app variable write does. The
-- trigger covers every writer: the config API, promotions and their
-- rollbacks, clones and GitOps apply.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- The delivered set: top-level keys that are environment variable names with a
-- string, number or boolean value (api.ProjectEnvironmentConfigEnv).
CREATE OR REPLACE FUNCTION project_environment_config_env(config jsonb)
RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT coalesce(jsonb_object_agg(e.key, e.value), '{}'::jsonb)
      FROM jsonb_each(CASE WHEN jsonb_typeof(config) = 'object' THEN config ELSE '{}'::jsonb END) e
     WHERE e.key ~ '^[A-Z][A-Z0-9_]*$' AND length(e.key) <= 128
       AND jsonb_typeof(e.value) IN ('string', 'number', 'boolean');
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_environment_config_runtime_change()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    previous jsonb;
BEGIN
    SELECT c.config_json INTO previous
      FROM project_environment_config_versions c
     WHERE c.project_id = NEW.project_id AND c.environment_slug = NEW.environment_slug
       AND c.version < NEW.version
     ORDER BY c.version DESC
     LIMIT 1;
    IF project_environment_config_env(previous) = project_environment_config_env(NEW.config_json) THEN
        RETURN NULL;
    END IF;
    -- The default scope reads production (workloadEnvironmentSlug).
    WITH member AS (
        SELECT a.id FROM apps a
         WHERE a.project_id = NEW.project_id AND a.account_id = NEW.account_id AND a.status <> 'deleted'
    ), scopes AS (
        SELECT unnest(CASE WHEN NEW.environment_slug = 'production'
            THEN ARRAY['production', 'default'] ELSE ARRAY[NEW.environment_slug] END) AS scope
    ), stamped AS (
        INSERT INTO app_runtime_config_scope_changes (app_id, scope, changed_at)
        SELECT member.id, scopes.scope, clock_timestamp() FROM member, scopes
        ON CONFLICT (app_id, scope) DO UPDATE
            SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at)
        RETURNING app_id, scope, changed_at
    )
    UPDATE snapshots SET stale = true
      FROM deployments d, stamped
     WHERE snapshots.deployment_id = d.id AND d.app_id = stamped.app_id
       AND coalesce(nullif(d.scope, ''), 'default') = stamped.scope
       AND NOT snapshots.stale AND snapshots.created_at <= stamped.changed_at;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS project_environment_config_runtime_change ON project_environment_config_versions;
CREATE TRIGGER project_environment_config_runtime_change
AFTER INSERT ON project_environment_config_versions
FOR EACH ROW EXECUTE FUNCTION project_environment_config_runtime_change();

-- +goose Down
DROP TRIGGER IF EXISTS project_environment_config_runtime_change ON project_environment_config_versions;
DROP FUNCTION IF EXISTS project_environment_config_runtime_change();
DROP FUNCTION IF EXISTS project_environment_config_env(jsonb);
