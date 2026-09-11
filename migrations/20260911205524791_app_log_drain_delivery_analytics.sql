-- filename: 20260911205524791_app_log_drain_delivery_analytics.sql

-- +goose Up
-- +goose StatementBegin

-- Cumulative hourly samples back the customer-facing log-drain analytics
-- surface. Cumulative values let gatewayd resume the series after restart;
-- the API computes bounded hourly deltas and rates at read time.
ALTER TABLE app_log_drain_health
    ADD COLUMN IF NOT EXISTS delivery_latency_nanos_total bigint NOT NULL DEFAULT 0
        CHECK (delivery_latency_nanos_total >= 0),
    ADD COLUMN IF NOT EXISTS delivery_latency_samples bigint NOT NULL DEFAULT 0
        CHECK (delivery_latency_samples >= 0);

CREATE TABLE IF NOT EXISTS app_log_drain_delivery_analytics (
    drain_id uuid NOT NULL REFERENCES app_log_drains(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    delivered_total bigint NOT NULL DEFAULT 0 CHECK (delivered_total >= 0),
    failed_total bigint NOT NULL DEFAULT 0 CHECK (failed_total >= 0),
    dropped_total bigint NOT NULL DEFAULT 0 CHECK (dropped_total >= 0),
    retries_total bigint NOT NULL DEFAULT 0 CHECK (retries_total >= 0),
    dead_letter_total bigint NOT NULL DEFAULT 0 CHECK (dead_letter_total >= 0),
    delivery_latency_nanos_total bigint NOT NULL DEFAULT 0
        CHECK (delivery_latency_nanos_total >= 0),
    delivery_latency_samples bigint NOT NULL DEFAULT 0
        CHECK (delivery_latency_samples >= 0),
    pending_records integer NOT NULL DEFAULT 0 CHECK (pending_records >= 0),
    pending_bytes bigint NOT NULL DEFAULT 0 CHECK (pending_bytes >= 0),
    sampled_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (drain_id, bucket_start)
);

CREATE INDEX IF NOT EXISTS app_log_drain_delivery_analytics_retention_idx
    ON app_log_drain_delivery_analytics (bucket_start);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_log_drain_delivery_analytics;
ALTER TABLE app_log_drain_health
    DROP COLUMN IF EXISTS delivery_latency_samples,
    DROP COLUMN IF EXISTS delivery_latency_nanos_total;
-- +goose StatementEnd
