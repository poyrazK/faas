-- filename: 20260911212130123_api_consumer_usage_ledger.sql
-- +goose Up
-- +goose StatementBegin

-- Customer API monetization foundation (ADR-120 follow-up): the gateway's
-- debug request_telemetry stream is intentionally droppable and retention-
-- limited, so it cannot be the financial source of truth. These two tables
-- keep an immutable event key alongside a minute aggregate. A retried event
-- is ignored by event_id before it can increment the aggregate a second time.
CREATE TABLE IF NOT EXISTS api_consumer_usage_events (
    event_id       uuid        PRIMARY KEY,
    account_id     uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id         uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_key   text        NOT NULL,
    window_start   timestamptz NOT NULL,
    request_count  bigint      NOT NULL CHECK (request_count > 0),
    error_count    bigint      NOT NULL CHECK (error_count >= 0 AND error_count <= request_count),
    billable_units bigint      NOT NULL CHECK (billable_units >= 0 AND billable_units <= request_count),
    received_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_usage_events_consumer_key_chk
        CHECK (consumer_key = '__anonymous__' OR consumer_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT api_consumer_usage_events_minute_chk
        CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);

CREATE TABLE IF NOT EXISTS api_consumer_usage_minutes (
    account_id     uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id         uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_key   text        NOT NULL,
    window_start   timestamptz NOT NULL,
    request_count  bigint      NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    error_count    bigint      NOT NULL DEFAULT 0 CHECK (error_count >= 0 AND error_count <= request_count),
    billable_units bigint      NOT NULL DEFAULT 0 CHECK (billable_units >= 0 AND billable_units <= request_count),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, app_id, consumer_key, window_start),
    CONSTRAINT api_consumer_usage_minutes_consumer_key_chk
        CHECK (consumer_key = '__anonymous__' OR consumer_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT api_consumer_usage_minutes_minute_chk
        CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);

CREATE INDEX IF NOT EXISTS api_consumer_usage_events_app_window_idx
    ON api_consumer_usage_events (app_id, consumer_key, window_start);
CREATE INDEX IF NOT EXISTS api_consumer_usage_minutes_account_window_idx
    ON api_consumer_usage_minutes (account_id, app_id, consumer_key, window_start);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS api_consumer_usage_minutes_account_window_idx;
DROP INDEX IF EXISTS api_consumer_usage_events_app_window_idx;
DROP TABLE IF EXISTS api_consumer_usage_minutes;
DROP TABLE IF EXISTS api_consumer_usage_events;
-- +goose StatementEnd
