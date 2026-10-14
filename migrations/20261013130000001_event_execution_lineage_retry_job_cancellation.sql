-- +goose Up
-- Preserve a later migration's wider allowed set when the ledger is replayed.
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conrelid='event_execution_lineage_retry_jobs'::regclass AND conname='event_execution_lineage_retry_jobs_state_check'
    AND pg_get_constraintdef(oid) LIKE '%''cancelled''%'
 ) THEN
  ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT IF EXISTS event_execution_lineage_retry_jobs_state_check;
  ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','completed','cancelled'));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Retained cancelled rows deliberately block downgrade; expire/prune them first.
-- Never change a cancelled job back to pending as part of a downgrade.
ALTER TABLE event_execution_lineage_retry_jobs DROP CONSTRAINT event_execution_lineage_retry_jobs_state_check;
ALTER TABLE event_execution_lineage_retry_jobs ADD CONSTRAINT event_execution_lineage_retry_jobs_state_check CHECK(state IN ('pending','completed'));
