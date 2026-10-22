-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_publisher_credentials (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
 token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
 channels text[] NOT NULL CHECK (cardinality(channels) BETWEEN 1 AND 32),
 event_types text[] NOT NULL CHECK (cardinality(event_types) BETWEEN 1 AND 16),
 publishes_per_minute integer NOT NULL CHECK (publishes_per_minute BETWEEN 1 AND 600),
 max_payload_bytes integer NOT NULL CHECK (max_payload_bytes BETWEEN 1 AND 4096),
 generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
 issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL CHECK (expires_at>issued_at),
 revoked_at timestamptz,
 rate_window timestamptz NOT NULL DEFAULT date_trunc('minute',clock_timestamp()),
 rate_count integer NOT NULL DEFAULT 0 CHECK (rate_count>=0),
 accepted_count bigint NOT NULL DEFAULT 0 CHECK (accepted_count>=0),
 replay_count bigint NOT NULL DEFAULT 0 CHECK (replay_count>=0),
 last_used_at timestamptz,
 last_channel text CHECK (last_channel IS NULL OR octet_length(last_channel) BETWEEN 1 AND 256),
 last_sequence bigint CHECK (last_sequence IS NULL OR last_sequence>0)
);
CREATE INDEX IF NOT EXISTS realtime_publisher_credentials_endpoint ON managed_realtime_publisher_credentials(endpoint_id,issued_at);
CREATE TABLE IF NOT EXISTS managed_realtime_publisher_receipts (
 credential_id uuid NOT NULL REFERENCES managed_realtime_publisher_credentials(id) ON DELETE CASCADE,
 idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 128),
 content_hash bytea NOT NULL CHECK (octet_length(content_hash)=32),
 channel text NOT NULL CHECK (octet_length(channel) BETWEEN 1 AND 256),
 sequence bigint NOT NULL CHECK (sequence>0),
 published_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(credential_id,idempotency_key),
 CHECK (expires_at>published_at)
);
-- +goose Down
DROP TABLE managed_realtime_publisher_receipts;
DROP TABLE managed_realtime_publisher_credentials;
