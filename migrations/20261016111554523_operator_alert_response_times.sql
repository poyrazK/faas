-- filename: 20261016111554523_operator_alert_response_times.sql

-- +goose Up
CREATE INDEX IF NOT EXISTS operator_alert_episodes_activation_idx
ON operator_alert_episodes (started_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS operator_alert_episodes_activation_idx;
