-- filename: 20261001090000001_exclusive_job_operations.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE exclusive_work_operations
  ALTER COLUMN app_id DROP NOT NULL,
  ADD COLUMN job_id uuid REFERENCES jobs(id),
  ADD CONSTRAINT exclusive_work_operations_target_check
    CHECK (num_nonnulls(app_id, job_id) = 1);

ALTER TABLE job_runs
  ADD COLUMN exclusive_operation_id uuid REFERENCES exclusive_work_operations(id) ON DELETE CASCADE,
  ADD COLUMN exclusive_generation bigint,
  ADD CONSTRAINT job_runs_exclusive_generation_check
    CHECK ((exclusive_operation_id IS NULL AND exclusive_generation IS NULL)
        OR (exclusive_operation_id IS NOT NULL AND exclusive_generation > 0));

CREATE UNIQUE INDEX job_runs_exclusive_generation_idx
  ON job_runs (exclusive_operation_id, exclusive_generation)
  WHERE exclusive_operation_id IS NOT NULL;

ALTER TABLE app_tasks
  ADD COLUMN exclusive_operation_id uuid REFERENCES exclusive_work_operations(id) ON DELETE CASCADE,
  ADD COLUMN exclusive_generation bigint,
  ADD CONSTRAINT app_tasks_exclusive_generation_check
    CHECK ((exclusive_operation_id IS NULL AND exclusive_generation IS NULL)
        OR (exclusive_operation_id IS NOT NULL AND exclusive_generation > 0));

CREATE INDEX app_tasks_exclusive_operation_idx
  ON app_tasks (exclusive_operation_id, exclusive_generation)
  WHERE exclusive_operation_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX job_runs_exclusive_generation_idx;
ALTER TABLE job_runs
  DROP CONSTRAINT job_runs_exclusive_generation_check,
  DROP COLUMN exclusive_generation,
  DROP COLUMN exclusive_operation_id;
DROP INDEX app_tasks_exclusive_operation_idx;
ALTER TABLE app_tasks
  DROP CONSTRAINT app_tasks_exclusive_generation_check,
  DROP COLUMN exclusive_generation,
  DROP COLUMN exclusive_operation_id;
ALTER TABLE exclusive_work_operations
  DROP CONSTRAINT exclusive_work_operations_target_check,
  DROP COLUMN job_id,
  ALTER COLUMN app_id SET NOT NULL;
-- +goose StatementEnd
