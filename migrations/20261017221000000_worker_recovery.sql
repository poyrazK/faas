-- Durable worker-pool reconciliation; app intent remains authoritative.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE worker_recovery (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    revision text NOT NULL CHECK (revision <> ''),
    claim_token uuid CHECK (claim_token <> '00000000-0000-0000-0000-000000000000'::uuid),
    lease_until timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    status text NOT NULL CHECK (status IN ('reconciling', 'ready', 'starting', 'draining', 'waiting_capacity', 'retrying_startup', 'waiting_dependency')),
    failures integer NOT NULL DEFAULT 0 CHECK (failures BETWEEN 0 AND 32),
    next_attempt_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (next_attempt_at >= updated_at OR claim_token IS NOT NULL)
);
CREATE INDEX worker_recovery_due_idx ON worker_recovery(next_attempt_at, app_id);

-- +goose Down
DROP TABLE worker_recovery;
