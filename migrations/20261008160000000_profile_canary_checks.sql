-- +goose Up
CREATE TABLE profile_canary_checks (
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    canary_step integer NOT NULL CHECK (canary_step >= 0),
    canary_step_started_at timestamptz NOT NULL,
    policy_revision bigint NOT NULL CHECK (policy_revision BETWEEN 1 AND 9007199254740991),
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object' AND octet_length(data::text) <= 98304),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'regressed', 'no_regression_detected', 'inconclusive', 'cancelled')),
    reason text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (deployment_id, canary_step, canary_step_started_at, policy_revision),
    CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((status IN ('queued', 'running')) = (completed_at IS NULL AND next_attempt_at IS NOT NULL))
);
CREATE INDEX profile_canary_checks_due_idx ON profile_canary_checks (next_attempt_at, created_at, deployment_id) WHERE status IN ('queued', 'running');
CREATE INDEX profile_canary_checks_retention_idx ON profile_canary_checks (completed_at, deployment_id) WHERE completed_at IS NOT NULL;

-- +goose Down
DROP TABLE profile_canary_checks;
