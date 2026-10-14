-- +goose Up
ALTER TABLE event_execution_coverage ADD COLUMN IF NOT EXISTS reconciliation_requested_at timestamptz;
CREATE INDEX IF NOT EXISTS event_execution_reconciliation_pending_idx
 ON event_execution_coverage(reconciliation_requested_at,subscription_id)
 WHERE reconciliation_requested_at IS NOT NULL AND NOT ledger_baseline_known;
-- +goose Down
DROP INDEX IF EXISTS event_execution_reconciliation_pending_idx;
ALTER TABLE event_execution_coverage DROP COLUMN IF EXISTS reconciliation_requested_at;
