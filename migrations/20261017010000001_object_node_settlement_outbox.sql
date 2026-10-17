-- +goose Up
-- ADR-1562: enroll at admission, publish evidence with terminal settlement.
-- Deliberately no foreign keys: delivery must survive source/bucket deletion.
CREATE TABLE IF NOT EXISTS object_node_settlement_outbox (
    id uuid PRIMARY KEY CHECK (id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    account_id uuid NOT NULL CHECK (account_id <> '00000000-0000-0000-0000-000000000000'),
    app_id uuid NOT NULL CHECK (app_id <> '00000000-0000-0000-0000-000000000000'),
    bucket_id uuid NOT NULL CHECK (bucket_id <> '00000000-0000-0000-0000-000000000000'),
    kind text NOT NULL CHECK (kind IN ('put', 'multipart')),
    backend_id text NOT NULL CHECK (backend_id <> ''),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint <> ''),
    physical_name text NOT NULL CHECK (physical_name <> ''),
    payload jsonb CHECK (payload IS NULL OR coalesce(
        jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 16384
        AND payload->>'id' = id::text AND payload->>'bucket' = physical_name AND payload->>'kind' = kind
        AND payload->>'status' IN ('committed', 'rejected', 'aborted')
        AND payload ?& ARRAY['id','bucket','key','kind','status','size','etag','manifest_hash']
        AND jsonb_typeof(payload->'size') = 'number' AND jsonb_typeof(payload->'key') = 'string'
        AND jsonb_typeof(payload->'etag') = 'string' AND jsonb_typeof(payload->'manifest_hash') = 'string', false)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    ready_at timestamptz CHECK (isfinite(ready_at)),
    retry_at timestamptz CHECK (isfinite(retry_at)),
    lease_token uuid CHECK (lease_token <> '00000000-0000-0000-0000-000000000000'),
    lease_until timestamptz CHECK (isfinite(lease_until)),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('', 'configuration', 'unavailable')),
    CONSTRAINT object_node_settlement_ready CHECK (
        (payload IS NULL AND ready_at IS NULL AND retry_at IS NULL AND lease_token IS NULL AND lease_until IS NULL AND attempts = 0 AND last_error = '')
        OR (payload IS NOT NULL AND ready_at IS NOT NULL AND retry_at IS NOT NULL AND isfinite(ready_at))),
    CONSTRAINT object_node_settlement_lease CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS object_node_settlement_due ON object_node_settlement_outbox (retry_at, id) WHERE ready_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS object_node_settlement_age ON object_node_settlement_outbox (ready_at) WHERE ready_at IS NOT NULL;

-- +goose Down
DROP TABLE object_node_settlement_outbox;
