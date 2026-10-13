-- ADR-1156: a workflow operation parked on a timer or approval is waiting.
-- +goose Up
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operations_state_check;
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_state_check
    CHECK (state IN ('accepted','running','waiting','succeeded','failed','cancelled','requires_reconciliation'));
-- Waiting work counts against the pending quota, like running work.
DROP INDEX IF EXISTS customer_operations_pending_account_idx;
CREATE INDEX IF NOT EXISTS customer_operations_pending_account_idx ON customer_operations(account_id)
    WHERE state IN ('accepted','running','waiting','requires_reconciliation');

-- +goose Down
-- A waiting row cannot satisfy the earlier constraint; fail loudly rather
-- than rewrite business state.
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operations_state_check;
ALTER TABLE customer_operations ADD CONSTRAINT customer_operations_state_check
    CHECK (state IN ('accepted','running','succeeded','failed','cancelled','requires_reconciliation'));
DROP INDEX IF EXISTS customer_operations_pending_account_idx;
CREATE INDEX IF NOT EXISTS customer_operations_pending_account_idx ON customer_operations(account_id)
    WHERE state IN ('accepted','running','requires_reconciliation');
