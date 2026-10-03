-- +goose Up
ALTER TABLE schedule_occurrences
  ADD COLUMN IF NOT EXISTS exclusive_operation_id uuid
    REFERENCES exclusive_work_operations(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS schedule_occurrences_exclusive_operation_idx
  ON schedule_occurrences (exclusive_operation_id)
  WHERE exclusive_operation_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS schedule_occurrences_exclusive_operation_idx;
ALTER TABLE schedule_occurrences
  DROP COLUMN IF EXISTS exclusive_operation_id;
