-- filename: 20261019233716417_project_config_default_scope_boundary.sql

-- ADR-1136 follow-up: a production project-configuration change stamped the
-- shared 'default' runtime boundary as well as 'production'. Every
-- environment's freshness check consults 'default' (default-scope app
-- variables are inherited by all scopes), so the change marked every
-- neighbouring environment's residents and snapshots stale. Stamp only the
-- changed environment; default-scope deployments, which read production's
-- configuration, still get their snapshots marked stale.
-- migration-compat: expand
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_environment_config_runtime_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous jsonb;
    changed timestamptz := clock_timestamp();
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
    WITH member AS (
        SELECT a.id FROM apps a
         WHERE a.project_id = NEW.project_id AND a.account_id = NEW.account_id AND a.status <> 'deleted'
    ), stamped AS (
        INSERT INTO app_runtime_config_scope_changes (app_id, scope, changed_at)
        SELECT member.id, NEW.environment_slug, changed FROM member
        ON CONFLICT (app_id, scope) DO UPDATE
            SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at)
        RETURNING app_id, scope, changed_at
    )
    UPDATE snapshots SET stale = true
      FROM deployments d, stamped
     WHERE snapshots.deployment_id = d.id AND d.app_id = stamped.app_id
       AND coalesce(nullif(d.scope, ''), 'default') = stamped.scope
       AND NOT snapshots.stale AND snapshots.created_at <= stamped.changed_at;
    -- The default scope reads production (workloadEnvironmentSlug): its
    -- snapshots go stale without advancing the shared 'default' boundary.
    IF NEW.environment_slug = 'production' THEN
        UPDATE snapshots SET stale = true
          FROM deployments d, apps a
         WHERE snapshots.deployment_id = d.id AND d.app_id = a.id
           AND a.project_id = NEW.project_id AND a.account_id = NEW.account_id AND a.status <> 'deleted'
           AND coalesce(nullif(d.scope, ''), 'default') = 'default'
           AND NOT snapshots.stale AND snapshots.created_at <= changed;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_environment_config_runtime_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
