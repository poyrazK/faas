-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE native_postgres_backup_schedules (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    owner_id text NOT NULL,
    org_id text NOT NULL,
    bucket_account_id uuid NOT NULL,
    bucket_app_id uuid NOT NULL,
    bucket_id uuid NOT NULL,
    utc_minute smallint NOT NULL CHECK (utc_minute BETWEEN 0 AND 1439),
    next_run_at timestamptz NOT NULL,
    retry_at timestamptz NOT NULL,
    run_id uuid,
    run_deadline timestamptz,
    export_queued boolean NOT NULL DEFAULT false,
    lease_token uuid,
    lease_until timestamptz,
    last_success_at timestamptz,
    last_archive_key text,
    last_error_code text,
    CHECK ((run_id IS NULL) = (run_deadline IS NULL)),
    CHECK (NOT export_queued OR run_id IS NOT NULL),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (lease_token IS NULL OR run_id IS NOT NULL),
    CHECK ((last_success_at IS NULL) = (last_archive_key IS NULL))
);

CREATE INDEX native_postgres_backup_schedules_due_idx
    ON native_postgres_backup_schedules (retry_at, database_id);

-- +goose Down
DROP TABLE native_postgres_backup_schedules;
