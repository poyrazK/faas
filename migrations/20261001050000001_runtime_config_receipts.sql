-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS instance_runtime_config_receipts (
    instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    wake_id uuid NOT NULL,
    scope text NOT NULL CHECK (scope <> '' AND length(scope) <= 64),
    boundary_at timestamptz NOT NULL CHECK (boundary_at >= 'epoch'::timestamptz),
    variables jsonb NOT NULL CHECK (jsonb_typeof(variables) = 'object' AND octet_length(variables::text) <= 1048576),
    secret_versions jsonb NOT NULL CHECK (jsonb_typeof(secret_versions) = 'object' AND octet_length(secret_versions::text) <= 1048576),
    all_secrets boolean NOT NULL,
    acknowledged_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS snapshot_runtime_config_receipts (
    snapshot_id uuid PRIMARY KEY REFERENCES snapshots(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope <> '' AND length(scope) <= 64),
    boundary_at timestamptz NOT NULL CHECK (boundary_at >= 'epoch'::timestamptz),
    variables jsonb NOT NULL CHECK (jsonb_typeof(variables) = 'object' AND octet_length(variables::text) <= 1048576),
    secret_versions jsonb NOT NULL CHECK (jsonb_typeof(secret_versions) = 'object' AND octet_length(secret_versions::text) <= 1048576),
    all_secrets boolean NOT NULL
);
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='environment_runtime_receipt_required' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION environment_runtime_receipt_required(target_app uuid, target_scope text)
RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1 FROM environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
    JOIN environment_gitops_resources r ON r.source_id = s.id
    WHERE r.app_id = target_app AND e.slug = target_scope
    AND (EXISTS (SELECT 1 FROM environment_managed_fields f WHERE f.source_id = s.id
        AND f.resource = r.logical_name AND f.field_path LIKE 'variables/%')
      OR EXISTS (SELECT 1 FROM environment_gitops_runtime_effects x WHERE x.source_id = s.id
        AND x.app_id = r.app_id AND x.completed_at IS NULL))
);
$$;$function$;
  END IF;
END $replay$;
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='environment_runtime_inputs_fresh' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION environment_runtime_inputs_fresh(target_app uuid, target_scope text, boundary timestamptz,
    observed_variables jsonb, observed_secrets jsonb, observed_all_secrets boolean)
RETURNS boolean LANGUAGE sql STABLE AS $$
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
$$;$function$;
  END IF;
END $replay$;
CREATE OR REPLACE VIEW environment_gitops_runtime_targets AS
WITH targets AS (
    SELECT s.id AS source_id, s.account_id, r.app_id, r.logical_name AS resource, e.slug AS environment_slug
    FROM environment_git_sources s
    JOIN project_environments e ON e.id = s.environment_id
    JOIN environment_gitops_resources r ON r.source_id = s.id
    JOIN apps a ON a.id = r.app_id AND a.account_id = s.account_id AND a.project_id = s.project_id
    WHERE EXISTS (SELECT 1 FROM environment_managed_fields f WHERE f.source_id = s.id
        AND f.resource = r.logical_name AND f.field_path LIKE 'variables/%')
       OR EXISTS (SELECT 1 FROM environment_gitops_runtime_effects x WHERE x.source_id = s.id
        AND x.app_id = r.app_id AND x.completed_at IS NULL)
), boundaries AS (
    SELECT t.*, greatest(
        coalesce((SELECT c.changed_at FROM app_runtime_config_changes c WHERE c.app_id = t.app_id), 'epoch'::timestamptz),
        coalesce((SELECT max(c.changed_at) FROM app_runtime_config_scope_changes c
            WHERE c.app_id = t.app_id AND c.scope IN ('default', t.environment_slug)), 'epoch'::timestamptz),
        coalesce((SELECT max(v.updated_at) FROM app_envs v JOIN environment_managed_fields f
            ON f.source_id = t.source_id AND f.resource = t.resource AND f.field_path = 'variables/' || v.key
            WHERE v.app_id = t.app_id AND v.scope = t.environment_slug), 'epoch'::timestamptz),
        coalesce((SELECT max(x.required_at) FROM environment_gitops_runtime_effects x
            WHERE x.source_id = t.source_id AND x.app_id = t.app_id AND x.completed_at IS NULL), 'epoch'::timestamptz)
    ) AS required_at FROM targets t
)
SELECT b.*,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting', 'running', 'warm', 'draining')
        AND NOT EXISTS (SELECT 1 FROM instance_runtime_config_receipts r WHERE r.instance_id = i.id AND r.wake_id = i.wake_id
            AND r.scope = b.environment_slug AND r.boundary_at >= b.required_at
            AND environment_runtime_inputs_fresh(b.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets))) AS stale_residents,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting')) AS starting_residents,
    (SELECT count(*) FROM snapshots p JOIN deployments d ON d.id = p.deployment_id
        WHERE d.app_id = b.app_id AND d.scope = b.environment_slug
        AND NOT p.stale AND NOT p.delete_pending
        AND NOT EXISTS (SELECT 1 FROM snapshot_runtime_config_receipts r WHERE r.snapshot_id = p.id
            AND r.scope = b.environment_slug AND r.boundary_at >= b.required_at
            AND environment_runtime_inputs_fresh(b.app_id, r.scope, r.boundary_at, r.variables, r.secret_versions, r.all_secrets))) AS stale_snapshots
FROM boundaries b;

-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
