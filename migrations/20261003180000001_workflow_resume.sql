-- +goose Up
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS resume_count integer NOT NULL DEFAULT 0;
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS cancelled_at timestamptz;
ALTER TABLE workflow_steps ADD COLUMN IF NOT EXISTS retry_base integer NOT NULL DEFAULT 0;
ALTER TABLE workflow_runs
    DROP CONSTRAINT IF EXISTS workflow_runs_resume_count_check,
    DROP CONSTRAINT IF EXISTS workflow_runs_cancelled_at_check,
    ADD CONSTRAINT workflow_runs_resume_count_check CHECK (resume_count BETWEEN 0 AND 16),
    ADD CONSTRAINT workflow_runs_cancelled_at_check CHECK (cancelled_at IS NULL OR status = 'failed');
ALTER TABLE workflow_steps
    DROP CONSTRAINT IF EXISTS workflow_steps_retry_base_check,
    ADD CONSTRAINT workflow_steps_retry_base_check CHECK (retry_base >= 0 AND retry_base <= attempt);
UPDATE workflow_runs SET cancelled_at = finished_at
WHERE status = 'failed' AND last_error = 'cancelled by operator';
CREATE TABLE IF NOT EXISTS workflow_run_resumes (
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
DROP TABLE IF EXISTS workflow_run_resumes;
ALTER TABLE workflow_steps DROP CONSTRAINT IF EXISTS workflow_steps_retry_base_check;
ALTER TABLE workflow_steps DROP COLUMN IF EXISTS retry_base;
ALTER TABLE workflow_runs
    DROP CONSTRAINT IF EXISTS workflow_runs_cancelled_at_check,
    DROP CONSTRAINT IF EXISTS workflow_runs_resume_count_check,
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS resume_count;
