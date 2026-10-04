-- +goose Up
-- ADR-531: retain private compute ownership before non-idempotent dispatch.
-- Endpoint availability grants no inventory, export/import or stage readiness.
CREATE TABLE project_environment_clone_postgres_copy_readers (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    capture_database_id uuid NOT NULL UNIQUE REFERENCES managed_postgres_databases(id),
    scope jsonb NOT NULL CHECK (jsonb_typeof(scope)='object'),
    owner_id uuid NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','requested','observed','deleting','retired')),
    request_started_at timestamptz CHECK (isfinite(request_started_at)),
    endpoint_id text CHECK (endpoint_id IS NULL OR length(endpoint_id) BETWEEN 1 AND 255),
    endpoint_created_at timestamptz CHECK (isfinite(endpoint_created_at)),
    available boolean NOT NULL DEFAULT false,
    observed_at timestamptz CHECK (isfinite(observed_at)),
    cleanup_requested_at timestamptz CHECK (isfinite(cleanup_requested_at)),
    cleanup_dispatched_at timestamptz CHECK (isfinite(cleanup_dispatched_at)),
    delete_operation_ids jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(delete_operation_ids)='array'),
    capture_operation_ids jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(capture_operation_ids)='array'),
    retired_at timestamptz CHECK (isfinite(retired_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY (operation_id,source_database_id),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_snapshot_restores(operation_id,source_database_id),
    CHECK (capture_database_id<>source_database_id AND owner_id<>capture_database_id AND owner_id<>source_database_id AND owner_id<>operation_id),
    CHECK ((endpoint_id IS NULL)=(endpoint_created_at IS NULL) AND (endpoint_id IS NULL)=(observed_at IS NULL)),
    CHECK (request_started_at IS NULL OR request_started_at>=created_at),
    CHECK (endpoint_created_at IS NULL OR (request_started_at IS NOT NULL AND endpoint_created_at>=request_started_at AND observed_at>=endpoint_created_at)),
    CHECK (cleanup_requested_at IS NULL OR cleanup_requested_at>=created_at),
    CHECK (cleanup_dispatched_at IS NULL OR (cleanup_requested_at IS NOT NULL AND cleanup_dispatched_at>=cleanup_requested_at AND endpoint_id IS NOT NULL)),
    CHECK (retired_at IS NULL OR retired_at>=cleanup_requested_at),
    CHECK (NOT available OR state='observed'),
    CHECK ((state='reserved' AND request_started_at IS NULL AND endpoint_id IS NULL AND cleanup_requested_at IS NULL AND retired_at IS NULL)
        OR (state='requested' AND request_started_at IS NOT NULL AND endpoint_id IS NULL AND cleanup_requested_at IS NULL AND retired_at IS NULL)
        OR (state='observed' AND request_started_at IS NOT NULL AND endpoint_id IS NOT NULL AND cleanup_requested_at IS NULL AND retired_at IS NULL)
        OR (state='deleting' AND cleanup_requested_at IS NOT NULL AND retired_at IS NULL)
        OR (state='retired' AND cleanup_requested_at IS NOT NULL AND retired_at IS NOT NULL AND
            ((request_started_at IS NULL AND endpoint_id IS NULL AND cleanup_dispatched_at IS NULL AND delete_operation_ids='[]' AND capture_operation_ids='[]')
            OR (request_started_at IS NOT NULL AND endpoint_id IS NOT NULL AND (delete_operation_ids<>'[]' OR capture_operation_ids<>'[]'))))),
    CHECK (state IN ('deleting','retired') OR (cleanup_dispatched_at IS NULL AND delete_operation_ids='[]' AND capture_operation_ids='[]'))
);
CREATE INDEX project_environment_clone_postgres_readers_account_holds
    ON project_environment_clone_postgres_copy_readers(account_id) WHERE state<>'retired';

-- +goose Down
-- Retired receipts still resolve committed replies and cannot be discarded.
ALTER TABLE project_environment_clone_postgres_copy_readers
    ADD CONSTRAINT postgres_copy_readers_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_copy_readers;
