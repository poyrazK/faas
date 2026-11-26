-- filename: 20261126195421797_api_sandbox_runs.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_runs (
    id uuid PRIMARY KEY,
    source_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    sandbox_app_id uuid UNIQUE REFERENCES apps(id) ON DELETE SET NULL,
    scenario_revision uuid NOT NULL,
    scenario_name text NOT NULL CHECK (char_length(scenario_name) BETWEEN 1 AND 100),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'passed', 'failed', 'interrupted')),
    steps jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(steps) = 'array'),
    failure_code text NOT NULL DEFAULT '',
    sandbox_deployment_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    share_hash bytea CHECK (share_hash IS NULL OR octet_length(share_hash) = 32),
    share_expires_at timestamptz,
    CHECK ((share_expires_at IS NULL) = (share_hash IS NULL)),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((status = 'running') = (lease_token IS NOT NULL)),
    CHECK ((status IN ('passed', 'failed', 'interrupted')) = (finished_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS api_sandbox_runs_source_idx
    ON api_sandbox_runs (source_deployment_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS api_sandbox_runs_work_idx
    ON api_sandbox_runs (created_at, id) WHERE status IN ('queued', 'running');

-- +goose Down
DROP TABLE IF EXISTS api_sandbox_runs;
