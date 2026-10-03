-- filename: 20260928221120084_app_webhook_live_claim_index.sql
-- +goose Up
-- The per-subscription concurrency cap counts only active claim leases.
-- Terminal deliveries stay out of this index as the ledger grows.
CREATE INDEX IF NOT EXISTS app_webhook_deliveries_live_claim_idx
    ON app_webhook_deliveries (webhook_id, next_attempt_at)
    WHERE status = 'in_flight';

-- +goose Down
-- Forward-only: the claim hot path depends on this index.
SELECT 1;
