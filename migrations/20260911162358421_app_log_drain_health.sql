-- filename: 20260911162358421_app_log_drain_health.sql
-- +goose Up
-- +goose StatementBegin

-- Customer-visible delivery state for the provider-neutral runtime log
-- drains. gatewayd-internal batches snapshots into this row; it never writes
-- on the hot application request path.
CREATE TABLE IF NOT EXISTS app_log_drain_health (
    drain_id uuid PRIMARY KEY REFERENCES app_log_drains(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('unknown', 'healthy', 'degraded', 'inactive')),
    active boolean NOT NULL DEFAULT false,
    queue_depth integer NOT NULL DEFAULT 0 CHECK (queue_depth >= 0),
    queue_capacity integer NOT NULL DEFAULT 0 CHECK (queue_capacity >= 0),
    delivered_total bigint NOT NULL DEFAULT 0 CHECK (delivered_total >= 0),
    failed_total bigint NOT NULL DEFAULT 0 CHECK (failed_total >= 0),
    dropped_total bigint NOT NULL DEFAULT 0 CHECK (dropped_total >= 0),
    retries_total bigint NOT NULL DEFAULT 0 CHECK (retries_total >= 0),
    stream_reconnects_total bigint NOT NULL DEFAULT 0 CHECK (stream_reconnects_total >= 0),
    gaps_total bigint NOT NULL DEFAULT 0 CHECK (gaps_total >= 0),
    last_success_at timestamptz,
    last_failure_at timestamptz,
    last_error text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The dashboard lists health alongside a bounded app's drains. Keep that
-- lookup independent from the primary-key join order.
CREATE INDEX IF NOT EXISTS app_log_drain_health_updated_idx
    ON app_log_drain_health (updated_at DESC, drain_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_log_drain_health;
-- +goose StatementEnd
