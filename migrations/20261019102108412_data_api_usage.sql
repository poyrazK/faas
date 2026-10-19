-- filename: 20261019102108412_data_api_usage.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock apps briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261019102108: hourly Data API usage per app, summed from runtime
-- deltas. Owner telemetry for usage views and owner caps, never billing input.
CREATE TABLE IF NOT EXISTS data_api_usage_hourly (
    app_id             uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    hour               timestamptz NOT NULL CHECK (date_bin('1 hour', hour, TIMESTAMPTZ '2000-01-01 00:00:00+00') = hour),
    user_requests      bigint NOT NULL DEFAULT 0 CHECK (user_requests >= 0),
    anonymous_requests bigint NOT NULL DEFAULT 0 CHECK (anonymous_requests >= 0),
    server_requests    bigint NOT NULL DEFAULT 0 CHECK (server_requests >= 0),
    rejected_requests  bigint NOT NULL DEFAULT 0 CHECK (rejected_requests >= 0),
    error_requests     bigint NOT NULL DEFAULT 0 CHECK (error_requests >= 0),
    capped_requests    bigint NOT NULL DEFAULT 0 CHECK (capped_requests >= 0),
    rows_returned      bigint NOT NULL DEFAULT 0 CHECK (rows_returned >= 0),
    response_bytes     bigint NOT NULL DEFAULT 0 CHECK (response_bytes >= 0),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, hour)
);
CREATE INDEX IF NOT EXISTS data_api_usage_hourly_hour_idx ON data_api_usage_hourly (hour);
COMMENT ON TABLE data_api_usage_hourly IS
    'ADR-20261019102108 Data API usage per app and UTC hour, from runtime deltas; owner telemetry, not billing.';

-- Report ids already applied, so a retried or snapshot-duplicated report
-- adds its deltas once.
CREATE TABLE IF NOT EXISTS data_api_usage_reports (
    app_id      uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    report_id   text NOT NULL CHECK (report_id ~ '^[A-Za-z0-9._:-]{1,128}$'),
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, report_id)
);
CREATE INDEX IF NOT EXISTS data_api_usage_reports_received_idx ON data_api_usage_reports (received_at);
COMMENT ON TABLE data_api_usage_reports IS
    'ADR-20261019102108 applied Data API usage report ids, kept for the dedup window.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS data_api_usage_reports;
DROP TABLE IF EXISTS data_api_usage_hourly;
-- +goose StatementEnd
