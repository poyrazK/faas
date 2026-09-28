-- +goose Up
-- A receiver's Retry-After pauses new claims for this subscription across
-- schedd instances. The deadline expires naturally; no sweep is required.
ALTER TABLE app_webhooks
    ADD COLUMN IF NOT EXISTS receiver_cooldown_until timestamptz;

-- +goose Down
-- Forward-only: removing this column while a dispatcher reads it would break
-- delivery claiming during a rolling downgrade.
SELECT 1;
