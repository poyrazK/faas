-- migration-compat: expand
-- filename: 20261031110141930_managed_postgres_overage_deliveries.sql
--
-- ADR-20261031110037: the managed PostgreSQL overage charge delivered to a
-- billing provider for a usage subject's UTC month. One row per provider,
-- subject and month holds the cumulative charge; apid advances it from the
-- previous value only, after the provider accepted the step (live) or
-- without sending it (shadow). apid is the only writer. A new table; the
-- previous release never reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS managed_postgres_overage_deliveries (
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{0,31}$'),
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL
        CHECK (period_start = (date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    live boolean NOT NULL,
    billed_unit_seconds bigint NOT NULL CHECK (billed_unit_seconds >= 0),
    delivered_millicents bigint NOT NULL CHECK (delivered_millicents > 0),
    updated_at timestamptz NOT NULL CHECK (isfinite(updated_at) AND updated_at >= period_start),
    PRIMARY KEY (provider, subject_id, period_start)
);
COMMENT ON TABLE managed_postgres_overage_deliveries IS 'Cumulative managed PostgreSQL overage charge delivered per billing provider, usage subject and UTC month; written only by apid (ADR-20261031110037).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS managed_postgres_overage_deliveries;
-- +goose StatementEnd
