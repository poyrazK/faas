-- filename: 20261010103000000_service_wake_ahead.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-956: a caller app opts into wake-ahead of the services it is measured
-- to call after it wakes. apid is the only writer; gatewayd-internal reads it.
CREATE TABLE IF NOT EXISTS app_service_wake_ahead (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at))
);
CREATE INDEX IF NOT EXISTS app_service_wake_ahead_account_idx ON app_service_wake_ahead (account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_service_wake_ahead;
-- +goose StatementEnd
