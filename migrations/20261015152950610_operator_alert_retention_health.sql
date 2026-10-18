-- filename: 20261015152950610_operator_alert_retention_health.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS operator_alert_retention_health (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    last_attempt_at timestamptz CHECK (isfinite(last_attempt_at)),
    last_success_at timestamptz CHECK (isfinite(last_success_at)),
    last_pruned_episodes bigint NOT NULL DEFAULT 0 CHECK (last_pruned_episodes >= 0),
    CHECK (last_success_at IS NULL OR (last_attempt_at IS NOT NULL AND last_success_at <= last_attempt_at))
);
INSERT INTO operator_alert_retention_health (singleton) VALUES (true) ON CONFLICT (singleton) DO NOTHING;

-- +goose Down
DROP TABLE operator_alert_retention_health;
