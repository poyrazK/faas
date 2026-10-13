-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_subscription_tickets (
 token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL CHECK (octet_length(principal) BETWEEN 1 AND 256),
 channels jsonb NOT NULL CHECK (jsonb_typeof(channels)='array' AND jsonb_array_length(channels) BETWEEN 1 AND 32),
 issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL CHECK (expires_at>issued_at AND expires_at<=issued_at+interval '5 minutes')
);
CREATE INDEX IF NOT EXISTS managed_realtime_subscription_tickets_endpoint_expiry ON managed_realtime_subscription_tickets(endpoint_id,expires_at);
-- +goose Down
DROP TABLE managed_realtime_subscription_tickets;
