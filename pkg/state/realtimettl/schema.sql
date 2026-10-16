-- Compile-time projection; runtime ownership: migration 20261012102700541.
CREATE TABLE managed_realtime_message_expirations (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL,
 sequence bigint NOT NULL,
 ttl_seconds integer NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY (endpoint_id, channel, sequence)
);

CREATE TABLE managed_realtime_schedules (endpoint_id uuid NOT NULL, channel text NOT NULL, schedule_id text NOT NULL, ttl_seconds integer NOT NULL);

-- Customer relay projection; runtime ownership: explicit pkg/commit customer schemas.
-- The optional ttl_seconds field is accessed through to_jsonb for old-schema compatibility.
CREATE TABLE public.gregale_realtime_outbox (event_id uuid PRIMARY KEY,endpoint_id text NOT NULL,channel text NOT NULL,data bytea NOT NULL,metadata jsonb NOT NULL,published_at timestamptz,blocked_code text,next_attempt_at timestamptz NOT NULL,lease_until timestamptz,lease_token uuid,attempts integer NOT NULL,created_at timestamptz NOT NULL);
CREATE TABLE public.gregale_commit_binding (singleton boolean NOT NULL,source_id uuid NOT NULL);

CREATE TABLE managed_realtime_publisher_credentials (id uuid PRIMARY KEY, max_message_ttl_seconds integer NOT NULL);

-- Runtime ownership: inbox TTL migration 20261012102700548.
CREATE TABLE managed_realtime_inbox_message_expirations (
 endpoint_id uuid NOT NULL,
 channel text NOT NULL,
 sequence bigint NOT NULL,
 ttl_seconds integer NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY (endpoint_id, channel, sequence)
);
