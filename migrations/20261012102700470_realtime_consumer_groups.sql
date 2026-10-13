-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_consumer_groups (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 channel text NOT NULL CHECK (octet_length(channel) BETWEEN 1 AND 256),
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
 assigned_sequence bigint NOT NULL CHECK (assigned_sequence >= 0),
 completed bigint NOT NULL DEFAULT 0 CHECK (completed >= 0),
 lease_seconds integer NOT NULL CHECK (lease_seconds BETWEEN 1 AND 300),
 max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 10),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(endpoint_id,channel,name)
);
CREATE TABLE IF NOT EXISTS managed_realtime_consumer_group_messages (
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 sequence bigint NOT NULL CHECK (sequence > 0),
 message jsonb NOT NULL CHECK (jsonb_typeof(message)='object'),
 status text NOT NULL CHECK (status IN ('ready','leased','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 worker text NOT NULL DEFAULT '' CHECK (octet_length(worker) <= 128),
 token_hash bytea CHECK (token_hash IS NULL OR octet_length(token_hash)=32),
 lease_until timestamptz,
 available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 failure_code text NOT NULL DEFAULT '' CHECK (octet_length(failure_code) <= 64),
 PRIMARY KEY(group_id,sequence),
 CHECK ((status='leased') = (token_hash IS NOT NULL AND lease_until IS NOT NULL)),
 CHECK (status='leased' OR (token_hash IS NULL AND lease_until IS NULL))
);
-- Each group's outstanding rows are admission bounded; this index orders claims.
CREATE INDEX IF NOT EXISTS realtime_consumer_group_claim ON managed_realtime_consumer_group_messages(group_id,status,sequence);
-- +goose Down
DROP TABLE managed_realtime_consumer_group_messages;
DROP TABLE managed_realtime_consumer_groups;
