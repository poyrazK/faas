-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS latest_anomaly_evaluation jsonb;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_rules DROP COLUMN latest_anomaly_evaluation;
