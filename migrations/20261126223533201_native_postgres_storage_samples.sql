-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS native_postgres_storage_samples (
    database_id uuid NOT NULL REFERENCES database_vms(database_id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('primary', 'standby')),
    node_id text NOT NULL,
    state_changed_at timestamptz NOT NULL,
    allocated_bytes bigint NOT NULL CHECK (allocated_bytes > 0),
    total_bytes bigint NOT NULL CHECK (total_bytes > 0),
    free_bytes bigint NOT NULL CHECK (free_bytes >= 0 AND free_bytes <= total_bytes),
    low boolean NOT NULL,
    read_only boolean NOT NULL,
    sampled_at timestamptz NOT NULL,
    PRIMARY KEY (database_id, role)
);
