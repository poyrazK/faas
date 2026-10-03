-- +goose Up
ALTER TABLE workflow_runs ADD COLUMN resume_count integer NOT NULL DEFAULT 0
    CHECK (resume_count BETWEEN 0 AND 16);
ALTER TABLE workflow_runs ADD COLUMN cancelled_at timestamptz
    CHECK (cancelled_at IS NULL OR status = 'failed');
UPDATE workflow_runs SET cancelled_at = finished_at
WHERE status = 'failed' AND last_error = 'cancelled by operator';
ALTER TABLE workflow_steps ADD COLUMN retry_base integer NOT NULL DEFAULT 0
    CHECK (retry_base >= 0 AND retry_base <= attempt);
CREATE TABLE workflow_run_resumes (
    run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    resume_number integer NOT NULL CHECK (resume_number BETWEEN 1 AND 16),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    previous_status text NOT NULL CHECK (previous_status IN ('failed', 'dead')),
    previous_error text,
    resumed_steps jsonb NOT NULL CHECK (jsonb_typeof(resumed_steps) = 'array' AND jsonb_array_length(resumed_steps) > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (run_id, resume_number)
);

-- +goose Down
-- Resumed runs must be drained before downgrade: older schedulers cannot honor
-- the retry budget or fencing. Export resume history before dropping it.
DROP TABLE workflow_run_resumes;
ALTER TABLE workflow_steps DROP COLUMN retry_base;
ALTER TABLE workflow_runs DROP COLUMN cancelled_at, DROP COLUMN resume_count;
