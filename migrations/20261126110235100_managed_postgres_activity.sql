-- filename: 20261126110235100_managed_postgres_activity.sql
-- migration-compat: expand
--
-- Why a managed PostgreSQL database is awake (ADR-20261125231222).
-- managed_postgres_compute_starts has one row per start of a database's
-- compute, read from the provider's operation log by the usage sweep, with its
-- likely cause; suspended_at is set when the matching suspend is read.
-- managed_postgres_awake_notices records the weekly notice so it is mailed
-- once. apid is the only writer; rows go with the database. An older apid
-- ignores both tables.
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_compute_starts (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    provider_operation_id text NOT NULL CHECK (provider_operation_id ~ '^[A-Za-z0-9_-]{1,128}$'),
    started_at timestamptz NOT NULL CHECK (isfinite(started_at)),
    suspended_at timestamptz CHECK (suspended_at IS NULL OR (isfinite(suspended_at) AND suspended_at >= started_at)),
    cause text NOT NULL CHECK (cause IN ('app_wake', 'cron', 'request', 'app_start', 'running_app', 'external')),
    cause_app_id uuid,
    cause_detail text NOT NULL DEFAULT '' CHECK (length(cause_detail) <= 512),
    recorded_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(recorded_at)),
    PRIMARY KEY (database_id, provider_operation_id),
    CHECK ((cause IN ('app_wake', 'external')) OR cause_app_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS managed_postgres_compute_starts_started_idx
    ON managed_postgres_compute_starts (database_id, started_at DESC);
CREATE INDEX IF NOT EXISTS managed_postgres_compute_starts_account_idx
    ON managed_postgres_compute_starts (account_id, started_at DESC);
CREATE INDEX IF NOT EXISTS managed_postgres_compute_starts_prune_idx
    ON managed_postgres_compute_starts (started_at);
COMMENT ON TABLE managed_postgres_compute_starts IS 'Starts of a managed PostgreSQL database''s compute with their likely cause (diagnosis, never billing); pruned after 30 days; written only by apid.';

-- One row per wake-with-app claim, so a start read an hour later still finds
-- the claim that caused it after later claims moved last_started_at.
CREATE TABLE IF NOT EXISTS managed_postgres_app_wake_starts (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    claimed_at timestamptz NOT NULL CHECK (isfinite(claimed_at)),
    PRIMARY KEY (database_id, claimed_at)
);
CREATE INDEX IF NOT EXISTS managed_postgres_app_wake_starts_prune_idx
    ON managed_postgres_app_wake_starts (claimed_at);
COMMENT ON TABLE managed_postgres_app_wake_starts IS 'Wake-with-app start claims, matched to recorded compute starts; pruned with them after 30 days; written only by apid.';

CREATE TABLE IF NOT EXISTS managed_postgres_awake_notices (
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    week_start date NOT NULL,
    awake_seconds bigint NOT NULL CHECK (awake_seconds >= 0),
    starts integer NOT NULL CHECK (starts >= 0),
    cause text NOT NULL CHECK (cause IN ('app_wake', 'cron', 'request', 'app_start', 'running_app', 'external')),
    cause_app_id uuid,
    cause_detail text NOT NULL DEFAULT '' CHECK (length(cause_detail) <= 512),
    cause_starts integer NOT NULL CHECK (cause_starts >= 0 AND cause_starts <= starts),
    recorded_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(recorded_at)),
    mailed_at timestamptz CHECK (mailed_at IS NULL OR isfinite(mailed_at)),
    PRIMARY KEY (database_id, week_start)
);
COMMENT ON TABLE managed_postgres_awake_notices IS 'Weekly notices that one cause kept a managed PostgreSQL database awake; mailed once; written only by apid.';

-- +goose Down
DROP TABLE IF EXISTS managed_postgres_awake_notices;
DROP TABLE IF EXISTS managed_postgres_app_wake_starts;
DROP TABLE IF EXISTS managed_postgres_compute_starts;
