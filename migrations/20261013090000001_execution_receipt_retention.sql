-- filename: 20261013090000001_execution_receipt_retention.sql

-- +goose Up
-- +goose StatementBegin
-- Terminal Run receipts (result, stdout/stderr, artifacts, replay events) are
-- deleted after api.ExecutionReceiptRetention. The usage ledger is the billing
-- record, so it must outlive the receipt: drop the cascading execution FK and
-- keep only the account FK.
ALTER TABLE execution_usage_ledger
    DROP CONSTRAINT IF EXISTS execution_usage_ledger_execution_id_fkey;

-- Supports the bounded retention sweep (oldest terminal receipts first).
CREATE INDEX IF NOT EXISTS executions_terminal_finished_idx
    ON executions (finished_at, id)
    WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS executions_terminal_finished_idx;
DELETE FROM execution_usage_ledger AS ledger
WHERE NOT EXISTS (SELECT 1 FROM executions WHERE executions.id = ledger.execution_id);
ALTER TABLE execution_usage_ledger
    DROP CONSTRAINT IF EXISTS execution_usage_ledger_execution_id_fkey;
ALTER TABLE execution_usage_ledger
    ADD CONSTRAINT execution_usage_ledger_execution_id_fkey
    FOREIGN KEY (execution_id) REFERENCES executions(id) ON DELETE CASCADE;
-- +goose StatementEnd
