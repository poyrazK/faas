-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_worker_credentials (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 group_id uuid NOT NULL REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
 token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
 permissions text[] NOT NULL CHECK (cardinality(permissions) BETWEEN 1 AND 9 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete']::text[]),
 generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
 issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL CHECK (expires_at > issued_at),
 revoked_at timestamptz,
 last_used_at timestamptz,
 last_operation text CHECK (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete')),
 use_count bigint NOT NULL DEFAULT 0 CHECK (use_count >= 0)
);
CREATE INDEX IF NOT EXISTS realtime_worker_credentials_group ON managed_realtime_worker_credentials(group_id,issued_at);
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS credential_id uuid;
-- Credential ownership is retained with a lease; no FK can cascade-away work.
-- +goose Down
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN credential_id;
DROP TABLE managed_realtime_worker_credentials;
