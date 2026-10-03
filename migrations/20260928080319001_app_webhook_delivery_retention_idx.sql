-- +goose Up
-- The retention worker selects a small oldest-first batch of terminal rows.
-- Active deliveries stay outside the index and are never pruned.
CREATE INDEX IF NOT EXISTS app_webhook_deliveries_retention_idx
    ON app_webhook_deliveries (updated_at, id)
    WHERE status IN ('succeeded', 'dead');

-- +goose Down
-- Forward-only: dropping the index would make retention scan the full ledger.
SELECT 1;
