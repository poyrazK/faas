-- filename: 20261020010936990_project_config_default_boundary_key.sql

-- ADR-1136 follow-up. A production project-configuration change must fence
-- default-scope deployments (they read production's configuration) without
-- marking neighbouring environments stale. Stamping the shared 'default'
-- boundary did the first but broke the second: every environment consults
-- 'default'. Default-scope deployments now also consult a dedicated
-- 'default:project-config' boundary that only this trigger stamps.
-- migration-compat: expand
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_runtime_base_inputs_fresh(target_app uuid, target_scope text, boundary timestamp with time zone, observed_variables jsonb, observed_secrets jsonb, observed_all_secrets boolean) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
SELECT
    NOT EXISTS (SELECT 1 FROM app_runtime_config_changes c WHERE c.app_id = target_app AND c.changed_at > boundary)
    AND NOT EXISTS (SELECT 1 FROM app_runtime_config_scope_changes c WHERE c.app_id = target_app
        AND (c.scope IN ('default', target_scope)
            -- ADR-1136: default-scope deployments read production's project configuration.
            OR (target_scope = 'default' AND c.scope = 'default:project-config'))
        AND c.changed_at > boundary)
    AND observed_variables = coalesce((SELECT jsonb_object_agg(v.key, v.value) FROM app_envs v
        WHERE v.app_id = target_app AND v.scope = target_scope), '{}'::jsonb)
    AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secrets) r WHERE NOT EXISTS (
        SELECT 1 FROM app_secrets v WHERE v.app_id = target_app AND v.scope = target_scope
        AND v.scope || '/' || v.key = r.key AND v.delivery_version::text = r.value))
    AND (NOT observed_all_secrets OR observed_secrets = coalesce((SELECT jsonb_object_agg(v.scope || '/' || v.key, v.delivery_version)
        FROM app_secrets v WHERE v.app_id = target_app AND v.scope = target_scope), '{}'::jsonb));
$$;
-- +goose StatementEnd

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
    -- The default scope reads production (workloadEnvironmentSlug). It gets
    -- its own boundary, never the shared 'default' one every environment
    -- consults, and its snapshots go stale.
    IF NEW.environment_slug = 'production' THEN
        INSERT INTO app_runtime_config_scope_changes (app_id, scope, changed_at)
        SELECT a.id, 'default:project-config', changed FROM apps a
         WHERE a.project_id = NEW.project_id AND a.account_id = NEW.account_id AND a.status <> 'deleted'
        ON CONFLICT (app_id, scope) DO UPDATE
            SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at);
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
CREATE OR REPLACE FUNCTION environment_runtime_base_inputs_fresh(target_app uuid, target_scope text, boundary timestamp with time zone, observed_variables jsonb, observed_secrets jsonb, observed_all_secrets boolean) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
SELECT
    NOT EXISTS (SELECT 1 FROM app_runtime_config_changes c WHERE c.app_id = target_app AND c.changed_at > boundary)
    AND NOT EXISTS (SELECT 1 FROM app_runtime_config_scope_changes c WHERE c.app_id = target_app
        AND c.scope IN ('default', target_scope) AND c.changed_at > boundary)
    AND observed_variables = coalesce((SELECT jsonb_object_agg(v.key, v.value) FROM app_envs v
        WHERE v.app_id = target_app AND v.scope = target_scope), '{}'::jsonb)
    AND NOT EXISTS (SELECT 1 FROM jsonb_each_text(observed_secrets) r WHERE NOT EXISTS (
        SELECT 1 FROM app_secrets v WHERE v.app_id = target_app AND v.scope = target_scope
        AND v.scope || '/' || v.key = r.key AND v.delivery_version::text = r.value))
    AND (NOT observed_all_secrets OR observed_secrets = coalesce((SELECT jsonb_object_agg(v.scope || '/' || v.key, v.delivery_version)
        FROM app_secrets v WHERE v.app_id = target_app AND v.scope = target_scope), '{}'::jsonb));
$$;
-- +goose StatementEnd

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
