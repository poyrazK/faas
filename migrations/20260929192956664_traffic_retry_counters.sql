-- filename: 20260929192956664_traffic_retry_counters.sql

-- +goose Up
-- ADR-531: gateways atomically observe originals and spend aggregate retries.
-- Process replacement never grants a fresh allowance in an active window.
CREATE TABLE IF NOT EXISTS traffic_retry_counters (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    originals bigint NOT NULL CHECK (originals > 0),
    retries bigint NOT NULL DEFAULT 0 CHECK (retries >= 0),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at))
);

-- +goose Down
DROP TABLE IF EXISTS traffic_retry_counters;
