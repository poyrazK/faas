-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_push_rate_reservations (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL,
 digest_id uuid NOT NULL,
 reserved_at timestamptz NOT NULL,
 PRIMARY KEY(endpoint_id,principal,digest_id)
);
CREATE INDEX IF NOT EXISTS managed_realtime_push_rate_reservations_window_idx ON managed_realtime_push_rate_reservations(endpoint_id,principal,reserved_at);
CREATE INDEX IF NOT EXISTS managed_realtime_push_rate_reservations_cleanup_idx ON managed_realtime_push_rate_reservations(reserved_at);
-- +goose Down
DROP TABLE managed_realtime_push_rate_reservations;
