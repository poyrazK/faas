-- filename: 20260930211607857_service_recovery.sql
-- ADR-420: scheduler-owned retries for steady service capacity.
-- +goose Up
CREATE TABLE IF NOT EXISTS service_recovery (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    revision text NOT NULL,
    claim_token uuid,
    lease_until timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    status text NOT NULL CHECK (status IN ('reconciling', 'ready', 'starting', 'draining', 'rolling_out', 'waiting_capacity', 'retrying_startup', 'waiting_dependency')),
    failures integer NOT NULL DEFAULT 0 CHECK (failures BETWEEN 0 AND 32),
    next_attempt_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS service_recovery_due_idx ON service_recovery(next_attempt_at, app_id);

-- +goose Down
DROP TABLE IF EXISTS service_recovery;
