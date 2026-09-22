-- +goose Up
-- +goose StatementBegin
-- Fleet-wide admission budget for the warm-saturation request queue.
-- Request bodies remain in gateway memory; this table stores only short-lived
-- permits so multiple gatewayd-internal replicas share one per-app depth cap.
CREATE TABLE IF NOT EXISTS gateway_concurrency_queue_leases (
    lease_id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_concurrency_queue_leases_app_expiry_idx
    ON gateway_concurrency_queue_leases (app_id, expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gateway_concurrency_queue_leases;
-- +goose StatementEnd
