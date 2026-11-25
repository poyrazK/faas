-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_query_snapshots (
    owner_id uuid PRIMARY KEY,
    observed_at timestamptz NOT NULL,
    active jsonb NOT NULL CHECK (jsonb_typeof(active) = 'array')
);

CREATE TABLE native_postgres_query_cancels (
    target_id uuid PRIMARY KEY,
    owner_id uuid NOT NULL,
    database_id uuid NOT NULL,
    node_id text NOT NULL,
    primary_since timestamptz NOT NULL,
    target jsonb NOT NULL,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    dispatched_at timestamptz,
    outcome text CHECK (outcome IN ('signal_sent', 'stale_target', 'unconfirmed'))
);

CREATE INDEX native_postgres_query_cancels_owner_pending
    ON native_postgres_query_cancels (owner_id, requested_at)
    WHERE dispatched_at IS NULL AND outcome IS NULL;
CREATE INDEX native_postgres_query_cancels_expiry ON native_postgres_query_cancels (requested_at);

-- +goose Down
DROP TABLE native_postgres_query_cancels;
DROP TABLE native_postgres_query_snapshots;
