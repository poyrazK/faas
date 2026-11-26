-- filename: 20261126170306845_api_sandbox_suites.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE api_sandbox_suites (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    revision uuid NOT NULL,
    scenario_ids uuid[] NOT NULL CHECK (cardinality(scenario_ids) <= 5 AND array_position(scenario_ids, NULL) IS NULL)
);

CREATE TABLE api_sandbox_suite_runs (
    id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    suite_revision uuid NOT NULL,
    baseline_deployment_id uuid NOT NULL,
    candidate_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    baseline_artifact_pin text NOT NULL CHECK (char_length(baseline_artifact_pin) = 64),
    candidate_artifact_pin text NOT NULL CHECK (char_length(candidate_artifact_pin) = 64),
    scenario_count smallint NOT NULL CHECK (scenario_count BETWEEN 1 AND 5),
    completed_count smallint NOT NULL DEFAULT 0 CHECK (completed_count BETWEEN 0 AND scenario_count),
    created_at timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL CHECK (deadline_at > created_at),
    finished_at timestamptz,
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    failure_code text NOT NULL DEFAULT '' CHECK (failure_code IN ('', 'timed_out', 'approval_inputs_changed', 'entitlement_changed')),
    CHECK (baseline_deployment_id <> candidate_deployment_id),
    CHECK ((next_attempt_at IS NULL) = (finished_at IS NOT NULL)),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (failure_code = '' OR finished_at IS NOT NULL)
);

CREATE UNIQUE INDEX api_sandbox_suite_runs_active_app ON api_sandbox_suite_runs (app_id) WHERE finished_at IS NULL;
CREATE INDEX api_sandbox_suite_runs_due ON api_sandbox_suite_runs (next_attempt_at, id) WHERE finished_at IS NULL;
CREATE INDEX api_sandbox_suite_runs_candidate ON api_sandbox_suite_runs (candidate_deployment_id, created_at DESC, id DESC);

CREATE TABLE api_sandbox_suite_scenarios (
    suite_run_id uuid NOT NULL REFERENCES api_sandbox_suite_runs(id) ON DELETE CASCADE,
    position smallint NOT NULL CHECK (position BETWEEN 0 AND 4),
    scenario_id uuid NOT NULL,
    scenario_revision uuid NOT NULL,
    scenario_name text NOT NULL CHECK (char_length(scenario_name) BETWEEN 1 AND 100),
    definition bytea NOT NULL CHECK (octet_length(definition) BETWEEN 2 AND 24576),
    comparison_id uuid NOT NULL UNIQUE,
    PRIMARY KEY (suite_run_id, position),
    UNIQUE (suite_run_id, scenario_id)
);

-- +goose Down
DROP TABLE api_sandbox_suite_scenarios;
DROP TABLE api_sandbox_suite_runs;
DROP TABLE api_sandbox_suites;
