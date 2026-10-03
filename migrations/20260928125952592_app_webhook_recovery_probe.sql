-- filename: 20260928125952592_app_webhook_recovery_probe.sql

-- +goose Up
-- +goose StatementBegin
-- A cooldown's first eligible delivery is a persisted recovery probe. The
-- delivery's existing claim deadline fences stale completion attempts.
ALTER TABLE app_webhooks
    ADD COLUMN IF NOT EXISTS receiver_recovery_probe_delivery_id uuid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only during rolling deployment: schedulers may still use the probe.
SELECT 1;
-- +goose StatementEnd
