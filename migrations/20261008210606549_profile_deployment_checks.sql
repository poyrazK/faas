-- +goose Up
CREATE TABLE IF NOT EXISTS profile_deployment_policies (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    enabled boolean NOT NULL,
    config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 8192),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS profile_deployment_checks (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    policy_revision bigint NOT NULL CHECK (policy_revision BETWEEN 1 AND 9007199254740991),
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object' AND octet_length(data::text) <= 8192),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'regressed', 'no_regression_detected', 'inconclusive', 'cancelled')),
    reason text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    investigation_id uuid REFERENCES profile_investigations(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((status IN ('queued', 'running')) = (completed_at IS NULL AND next_attempt_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS profile_deployment_checks_due_idx ON profile_deployment_checks (next_attempt_at, deployment_id) WHERE status IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS profile_deployment_checks_app_created_idx ON profile_deployment_checks (app_id, created_at DESC, deployment_id);
CREATE INDEX IF NOT EXISTS profile_deployment_checks_completed_idx ON profile_deployment_checks (completed_at, deployment_id) WHERE completed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS deployments_profile_completed_idx ON deployments (app_id, scope, rollout_completed_at DESC, id DESC)
    WHERE status IN ('live', 'superseded') AND rollout_state = 'complete' AND deleted_at IS NULL;

-- +goose Down
DROP INDEX deployments_profile_completed_idx;
DROP TABLE profile_deployment_checks;
DROP TABLE profile_deployment_policies;
