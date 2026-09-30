-- +goose Up
-- +goose StatementBegin
CREATE TABLE app_runtime_config_scope_changes (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope <> '' AND length(scope) <= 64),
    changed_at timestamptz NOT NULL CHECK (changed_at >= 'epoch'::timestamptz),
    PRIMARY KEY (app_id, scope)
);
-- Snapshot publication and either kind of stamp order through the same app.
CREATE TRIGGER app_runtime_config_scope_change_lock_app
BEFORE INSERT OR UPDATE ON app_runtime_config_scope_changes
FOR EACH ROW EXECUTE FUNCTION app_runtime_config_change_lock_app();
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
        AND (i.started_at IS NULL OR i.started_at <= b.required_at)) AS stale_residents,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting')) AS starting_residents,
    (SELECT count(*) FROM snapshots p JOIN deployments d ON d.id = p.deployment_id
        WHERE d.app_id = b.app_id AND d.scope = b.environment_slug
        AND NOT p.stale AND NOT p.delete_pending AND p.created_at <= b.required_at) AS stale_snapshots
FROM boundaries b;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
        AND (i.started_at IS NULL OR i.started_at <= b.required_at)) AS stale_residents,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('waking', 'cold_booting')) AS starting_residents,
    (SELECT count(*) FROM snapshots p JOIN deployments d ON d.id = p.deployment_id
        WHERE d.app_id = b.app_id AND d.scope = b.environment_slug
        AND NOT p.stale AND NOT p.delete_pending AND p.created_at <= b.required_at) AS stale_snapshots
FROM boundaries b;
DROP TABLE app_runtime_config_scope_changes;
-- +goose StatementEnd
