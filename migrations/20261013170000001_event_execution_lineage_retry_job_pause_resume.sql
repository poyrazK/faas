-- +goose Up
-- Preserve a later migration's wider allowed set when the ledger is replayed.
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conrelid='event_execution_lineage_retry_jobs'::regclass AND conname='event_execution_lineage_retry_jobs_state_check'
    AND pg_get_constraintdef(oid) LIKE '%''paused''%'
 ) THEN
  ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT IF EXISTS event_execution_lineage_retry_jobs_state_check;
  ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','paused','completed','cancelled'));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Retained paused rows block downgrade. Deliberately resume/cancel them or expire/prune first.
-- Never resume work implicitly as part of a downgrade.
ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT event_execution_lineage_retry_jobs_state_check;
ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','completed','cancelled'));
