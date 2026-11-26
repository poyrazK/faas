-- filename: 20261126200000000_api_sandbox_suite_automation.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_sandbox_suites ADD COLUMN run_before_approval boolean NOT NULL DEFAULT false;
ALTER TABLE api_sandbox_suite_runs ADD COLUMN automatic boolean NOT NULL DEFAULT false,
    ADD COLUMN waiting_for_capacity boolean NOT NULL DEFAULT false;

CREATE TABLE api_sandbox_suite_auto_requests (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    run_id uuid NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN (
        'queued', 'waiting_for_capacity', 'waiting_for_baseline', 'waiting_for_scenarios', 'unavailable',
        'started', 'approval_ended', 'disabled'
    )),
    next_attempt_at timestamptz,
    CHECK ((next_attempt_at IS NULL) = (status IN ('started', 'approval_ended', 'disabled')))
);

CREATE INDEX api_sandbox_suite_auto_requests_due ON api_sandbox_suite_auto_requests (next_attempt_at, deployment_id)
    WHERE next_attempt_at IS NOT NULL;

-- +goose Down
DROP TABLE api_sandbox_suite_auto_requests;
ALTER TABLE api_sandbox_suite_runs DROP COLUMN waiting_for_capacity, DROP COLUMN automatic;
ALTER TABLE api_sandbox_suites DROP COLUMN run_before_approval;
