-- +goose Up
-- +goose StatementBegin
CREATE TABLE environment_gitops_runtime_effects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    revision_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    intent_version bigint NOT NULL CHECK (intent_version >= 0),
    plan_hash text NOT NULL CHECK (plan_hash ~ '^[a-f0-9]{64}$'),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_slug text NOT NULL CHECK (environment_slug <> ''),
    required_at timestamptz NOT NULL CHECK (required_at >= 'epoch'::timestamptz),
    wake_id uuid NOT NULL DEFAULT gen_random_uuid(),
    requested_at timestamptz,
    next_request_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (source_id, revision_id) REFERENCES environment_desired_revisions(source_id, id),
    UNIQUE (source_id, generation, plan_hash, app_id)
);
CREATE INDEX environment_gitops_runtime_effects_pending_idx ON environment_gitops_runtime_effects(source_id, app_id) WHERE completed_at IS NULL;

-- One statement snapshot of managed runtime boundaries and scoped residency.
-- Pending effects retain removed variable fields until their runtime work is
-- complete. A request acknowledgement alone is deliberately not a proof here.
CREATE VIEW environment_gitops_runtime_targets AS
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
        AND i.state IN ('WAKING', 'COLD_BOOTING', 'RUNNING', 'WARM', 'DRAINING')
        AND (i.started_at IS NULL OR i.started_at <= b.required_at)) AS stale_residents,
    (SELECT count(*) FROM instances i JOIN deployments d ON d.id = i.deployment_id
        WHERE i.app_id = b.app_id AND d.scope = b.environment_slug
        AND i.state IN ('WAKING', 'COLD_BOOTING')) AS starting_residents,
    (SELECT count(*) FROM snapshots p JOIN deployments d ON d.id = p.deployment_id
        WHERE d.app_id = b.app_id AND d.scope = b.environment_slug
        AND NOT p.stale AND NOT p.delete_pending AND p.created_at <= b.required_at) AS stale_snapshots
FROM boundaries b;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW environment_gitops_runtime_targets;
DROP TABLE environment_gitops_runtime_effects;
-- +goose StatementEnd
