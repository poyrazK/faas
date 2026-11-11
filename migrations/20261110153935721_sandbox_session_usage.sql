-- filename: 20261110153935721_sandbox_session_usage.sql
-- migration-compat: expand

-- +goose Up
-- The foreign keys lock sandbox_sessions and accounts briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 step 11: vmmd's measured memory of each sandbox session
-- VM (ADR-20261027090011 Measurement, quiet-paused and frozen seconds not
-- billed). vmmd reports it cumulatively while the session runs; agentd keeps
-- the latest record, fenced on the session's lease. Nothing bills it yet.
-- MB is MiB and *_mb_ms sums MB × milliseconds. A new table, so apid and
-- older agentd keep working while this lands.
CREATE TABLE IF NOT EXISTS sandbox_session_usage (
    sandbox_id             uuid PRIMARY KEY REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
    account_id             uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    method                 smallint NOT NULL CHECK (method >= 1),
    samples                integer NOT NULL CHECK (samples >= 0),
    expected_samples       integer NOT NULL CHECK (expected_samples >= samples),
    measured_ms            bigint NOT NULL CHECK (measured_ms >= 0),
    paused_ms              bigint NOT NULL CHECK (paused_ms BETWEEN 0 AND measured_ms),
    frozen_ms              bigint NOT NULL CHECK (frozen_ms BETWEEN 0 AND paused_ms),
    private_mb_ms          bigint NOT NULL CHECK (private_mb_ms >= 0),
    unpaused_private_mb_ms bigint NOT NULL CHECK (unpaused_private_mb_ms BETWEEN 0 AND private_mb_ms),
    billable_mb_ms         bigint NOT NULL CHECK (billable_mb_ms >= 0),
    peak_private_mb        bigint NOT NULL CHECK (peak_private_mb >= 0),
    cold_boot              boolean NOT NULL,
    final                  boolean NOT NULL DEFAULT false,
    recorded_at            timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sandbox_session_usage_account_idx ON sandbox_session_usage (account_id, updated_at DESC);
COMMENT ON TABLE sandbox_session_usage IS
    'ADR-20261027090013 step 11 measured private memory of sandbox session VMs, cumulative, written by agentd from vmmd''s usage frames; not billed yet.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_session_usage;
-- +goose StatementEnd
