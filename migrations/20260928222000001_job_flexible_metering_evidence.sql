-- +goose Up
ALTER TABLE usage_minutes
    ADD COLUMN job_run_id uuid,
    ADD COLUMN job_execution_class text;

UPDATE usage_minutes u SET
    job_run_id = r.id,
    job_execution_class = r.execution_class
FROM job_tasks t JOIN job_runs r ON r.id = t.run_id
WHERE u.meter_kind = 'job' AND t.instance_id = u.instance_id;

UPDATE usage_minutes u SET
    job_run_id = r.id,
    job_execution_class = r.execution_class
FROM job_task_attempts a JOIN job_runs r ON r.id = a.run_id
WHERE u.meter_kind = 'job' AND u.job_run_id IS NULL AND a.instance_id = u.instance_id;

-- Older usage rows predate flexible execution; retain their original charge
-- and mark them as standard even when instance linkage has aged out.
UPDATE usage_minutes SET job_execution_class = 'standard'
WHERE meter_kind = 'job' AND job_execution_class IS NULL;

ALTER TABLE usage_minutes ADD CONSTRAINT usage_minutes_job_execution_class_check CHECK (
    (meter_kind = 'app' AND job_execution_class IS NULL AND job_run_id IS NULL) OR
    (meter_kind = 'job' AND job_execution_class IN ('standard', 'flexible')));

CREATE INDEX usage_minutes_job_class_window_idx
    ON usage_minutes (account_id, job_execution_class, minute DESC)
    WHERE meter_kind = 'job';

-- +goose Down
DROP INDEX usage_minutes_job_class_window_idx;
ALTER TABLE usage_minutes DROP CONSTRAINT usage_minutes_job_execution_class_check;
ALTER TABLE usage_minutes DROP COLUMN job_execution_class, DROP COLUMN job_run_id;
