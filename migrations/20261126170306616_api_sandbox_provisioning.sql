-- filename: 20261126170306616_api_sandbox_provisioning.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_provisioning (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    smoke_path text NOT NULL DEFAULT '' CHECK (octet_length(smoke_path) <= 512),
    phase text NOT NULL DEFAULT 'preparing_database'
        CHECK (phase IN ('preparing_database', 'running_migrations', 'starting_release', 'verifying_api', 'live', 'failed')),
    started_at timestamptz NOT NULL DEFAULT now(),
    next_attempt_at timestamptz DEFAULT now(),
    lease_token uuid,
    lease_until timestamptz,
    deployment_id uuid,
    failure jsonb,
    verification jsonb,
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((phase IN ('live', 'failed')) = (next_attempt_at IS NULL)),
    CHECK ((phase = 'live') = (verification IS NOT NULL)),
    CHECK (verification IS NULL OR (
        deployment_id IS NOT NULL AND jsonb_typeof(verification) = 'object'
        AND (verification->>'deployment_id') IS NOT DISTINCT FROM deployment_id::text
    ))
);

CREATE INDEX IF NOT EXISTS api_sandbox_provisioning_pending_idx
    ON api_sandbox_provisioning (next_attempt_at, app_id)
    WHERE next_attempt_at IS NOT NULL;

INSERT INTO api_sandbox_provisioning (app_id)
SELECT id FROM apps
WHERE status = 'active' AND preview_pr_state = 'open' AND preview_expires_at > now()
  AND starts_with(preview_of_slug, 'api-sandbox:')
  AND manifest->>'api_sandbox_source_deployment_id' <> ''
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS api_sandbox_provisioning;
