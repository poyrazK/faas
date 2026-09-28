-- +goose Up
ALTER TABLE job_runs
    ADD COLUMN failure_policy text NOT NULL DEFAULT 'continue',
    ADD CONSTRAINT job_runs_failure_policy_check
        CHECK (failure_policy IN ('continue', 'fail_fast'));

-- +goose Down
ALTER TABLE job_runs
    DROP CONSTRAINT job_runs_failure_policy_check,
    DROP COLUMN failure_policy;
