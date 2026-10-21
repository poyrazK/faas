-- +goose Up
-- Preserve a later migration's wider allowed set when the ledger is replayed.
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conrelid='event_execution_lineage_retry_control_receipts'::regclass AND conname='event_execution_lineage_retry_control_receipts_action_check'
    AND pg_get_constraintdef(oid) LIKE '%''cancel''%'
 ) THEN
  ALTER TABLE event_execution_lineage_retry_control_receipts DROP CONSTRAINT IF EXISTS event_execution_lineage_retry_control_receipts_action_check;
  ALTER TABLE event_execution_lineage_retry_control_receipts ADD CONSTRAINT event_execution_lineage_retry_control_receipts_action_check CHECK(action IN ('pause','resume','cancel'));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- The narrower CHECK deliberately refuses downgrade while cancellation receipts
-- remain. Stop UUID-keyed cancellations and expire/prune their parent jobs first;
-- do not discard retained command deduplication evidence.
ALTER TABLE event_execution_lineage_retry_control_receipts
 DROP CONSTRAINT event_execution_lineage_retry_control_receipts_action_check,
 ADD CONSTRAINT event_execution_lineage_retry_control_receipts_action_check
 CHECK(action IN ('pause','resume'));
