-- filename: 20261102183146844_job_run_log_retention.sql
-- migration-compat: expand
-- ADR-20261102183152: a job run's stored stdout/stderr (job_tasks.log_content
-- and the copy the attempt journal keeps in job_task_attempts.log_content)
-- follows the account plan's log retention. meterd clears it one finished run
-- at a time and records the run's finished_at in logs_cleared_finished_at. A
-- run a retry reopens and finishes again gets a new finished_at, so it is due
-- again; a cleared run leaves the partial index, so a pass never revisits
-- cleared history.
--
-- The column is nullable without a default (a catalog-only change) and the
-- previous release neither reads nor writes it. The index is built
-- concurrently (NO TRANSACTION), so job_runs keeps taking writes. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE job_runs ADD COLUMN IF NOT EXISTS logs_cleared_finished_at timestamptz;

CREATE INDEX CONCURRENTLY IF NOT EXISTS job_runs_log_retention_idx
    ON job_runs (finished_at, id)
    WHERE finished_at IS NOT NULL AND logs_cleared_finished_at IS DISTINCT FROM finished_at;

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('job_runs_log_retention_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'job_runs_log_retention_idx is invalid: DROP INDEX CONCURRENTLY job_runs_log_retention_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS job_runs_log_retention_idx;
ALTER TABLE job_runs DROP COLUMN IF EXISTS logs_cleared_finished_at;
RESET lock_timeout;
