-- +goose Up
-- Raw compute heartbeats are an operational troubleshooting window, not the
-- long-term capacity ledger. Keep compact hourly aggregates before bounded
-- deletion removes raw samples.
CREATE TABLE compute_node_heartbeat_hourly (
    node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    bucket_at timestamptz NOT NULL,
    sample_count bigint NOT NULL CHECK (sample_count > 0),
    cpu_sample_count bigint NOT NULL DEFAULT 0 CHECK (cpu_sample_count >= 0),
    cpu_pct_sum double precision NOT NULL DEFAULT 0,
    disk_used_max_bytes bigint,
    first_received_at timestamptz NOT NULL,
    last_received_at timestamptz NOT NULL,
    last_heartbeat_at timestamptz NOT NULL,
    PRIMARY KEY (node_id, bucket_at),
    CHECK (bucket_at = date_trunc('hour', bucket_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    CHECK (first_received_at <= last_received_at)
);

CREATE INDEX compute_node_heartbeat_hourly_bucket_idx
    ON compute_node_heartbeat_hourly (bucket_at DESC);

-- Makes each SKIP LOCKED retention batch an ordered index walk instead of a
-- table scan while preserving the existing per-node history index.
CREATE INDEX compute_node_heartbeats_received_at_idx
    ON compute_node_heartbeats (received_at, id);

-- +goose Down
DROP INDEX IF EXISTS compute_node_heartbeats_received_at_idx;
DROP TABLE IF EXISTS compute_node_heartbeat_hourly;
