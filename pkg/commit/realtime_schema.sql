-- Install explicitly in the customer database alongside schema.sql and the
-- existing gregale_commit_binding. No platform migration runs on customer DBs.
CREATE TABLE public.gregale_realtime_outbox (
    event_id uuid PRIMARY KEY,
    endpoint_id text NOT NULL CHECK (endpoint_id ~ '^[0-9a-f]{32}$'),
    channel text NOT NULL CHECK (octet_length(channel) BETWEEN 1 AND 256),
    data bytea NOT NULL CHECK (octet_length(data) <= 4096),
    ttl_seconds integer NOT NULL DEFAULT coalesce(nullif(current_setting('gregale.realtime_ttl_seconds', true), '')::integer, 0) CHECK (ttl_seconds BETWEEN 0 AND 86400),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_at timestamptz,
    sequence bigint CHECK (sequence > 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    blocked_code text,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_token uuid,
    lease_until timestamptz,
    CHECK ((published_at IS NULL) = (sequence IS NULL))
);
CREATE INDEX gregale_realtime_outbox_pending
 ON public.gregale_realtime_outbox(next_attempt_at,created_at,event_id)
 WHERE published_at IS NULL AND blocked_code IS NULL;
