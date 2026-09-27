-- filename: 20260927212142268_app_webhook_delivery_health_indexes.sql

-- +goose Up
-- Scope health aggregation to one subscription without scanning an account's
-- whole delivery ledger. The second index finds the fleet's oldest due row.
CREATE INDEX IF NOT EXISTS app_webhook_deliveries_webhook_idx
    ON app_webhook_deliveries (webhook_id);
CREATE INDEX IF NOT EXISTS app_webhook_deliveries_due_idx
    ON app_webhook_deliveries (next_attempt_at)
    WHERE status IN ('pending', 'in_flight');

-- +goose Down
-- Forward-only: the health read path depends on these indexes.
SELECT 1;
