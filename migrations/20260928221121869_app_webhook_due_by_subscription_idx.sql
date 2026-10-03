-- filename: 20260928221121869_app_webhook_due_by_subscription_idx.sql
-- +goose Up
-- Claim a bounded number of oldest due rows for each selected subscription.
-- Terminal delivery history stays out of the scheduler's per-hook read.
CREATE INDEX IF NOT EXISTS app_webhook_deliveries_due_by_subscription_idx
    ON app_webhook_deliveries (webhook_id, next_attempt_at, id)
    WHERE status IN ('pending', 'in_flight');

-- +goose Down
-- Forward-only: the fair claim path depends on this index.
SELECT 1;
