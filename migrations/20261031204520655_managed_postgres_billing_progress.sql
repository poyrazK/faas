-- filename: 20261031204520655_managed_postgres_billing_progress.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261031165104: managed PostgreSQL overage is billed in daily
-- month-to-date steps, and a month closes once its usage has settled. One row
-- per provider, usage subject (an account, or an organization that owns
-- resources), UTC month and mode holds the cumulative charge delivered and at
-- most one pending step; apid writes it with a compare-and-set on version.
-- closed_at is set by the month's final step.
CREATE TABLE IF NOT EXISTS managed_postgres_billing_progress (
    provider             text NOT NULL CHECK (provider <> ''),
    subject_id           uuid NOT NULL,
    period_start         timestamptz NOT NULL CHECK (
        period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
    ),
    mode                 text NOT NULL CHECK (mode IN ('shadow', 'live')),
    delivered_millicents bigint NOT NULL CHECK (delivered_millicents >= 0),
    pending_millicents   bigint NOT NULL CHECK (pending_millicents >= 0),
    pending_fixed        boolean NOT NULL,
    closed_at            timestamptz,
    version              bigint NOT NULL CHECK (version > 0),
    updated_at           timestamptz NOT NULL,
    PRIMARY KEY (provider, subject_id, period_start, mode),
    CHECK (NOT pending_fixed OR pending_millicents > 0)
);

COMMENT ON TABLE managed_postgres_billing_progress IS
    'Managed PostgreSQL overage charge delivered so far per provider, usage subject, UTC month and mode. Written by apid in daily steps and at month close (ADR-20261031165104).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS managed_postgres_billing_progress;
