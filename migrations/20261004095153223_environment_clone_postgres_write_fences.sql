-- +goose Up
-- ADR-531: reserve recovery authority before any remote database closure.
-- Held/abandoning rows block source deletion even when dispatch is unknown.
CREATE TABLE project_environment_clone_postgres_write_fences (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
    source_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE RESTRICT,
    source_version text NOT NULL CHECK (source_version ~ '^[a-f0-9]{64}$'),
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    source_provider_resource_id text NOT NULL CHECK (source_provider_resource_id<>'' AND length(source_provider_resource_id)<=255),
    source_data_resource_id text NOT NULL CHECK (source_data_resource_id<>'' AND length(source_data_resource_id)<=255),
    state text NOT NULL DEFAULT 'held' CHECK (state IN ('held','abandoning','released')),
    remote_terminal_state text CHECK (remote_terminal_state IS NULL OR remote_terminal_state IN ('released','abandoned')),
    remote_released_at timestamptz,
    released_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (operation_id,source_database_id),
    CHECK ((state='released' AND remote_terminal_state IS NOT NULL AND remote_released_at IS NOT NULL AND released_at IS NOT NULL)
        OR (state<>'released' AND remote_terminal_state IS NULL AND remote_released_at IS NULL AND released_at IS NULL))
);
CREATE UNIQUE INDEX project_environment_clone_postgres_write_fence_source_owner
    ON project_environment_clone_postgres_write_fences(source_database_id) WHERE state<>'released';
CREATE UNIQUE INDEX project_environment_clone_postgres_write_fence_dataset_owner
    ON project_environment_clone_postgres_write_fences(backend_id,backend_fingerprint,source_data_resource_id) WHERE state<>'released';

-- +goose Down
DROP TABLE project_environment_clone_postgres_write_fences;
