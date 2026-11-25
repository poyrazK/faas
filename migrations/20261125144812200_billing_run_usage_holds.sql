-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE billing_run_items
    ADD COLUMN IF NOT EXISTS usage_gap_hours jsonb,
    ADD COLUMN IF NOT EXISTS usage_held_at timestamptz,
    ADD COLUMN IF NOT EXISTS usage_recovered_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE billing_run_items
    DROP COLUMN usage_recovered_at,
    DROP COLUMN usage_held_at,
    DROP COLUMN usage_gap_hours;
