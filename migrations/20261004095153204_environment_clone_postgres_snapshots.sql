-- +goose Up
-- ADR-531: persist ownership before provider IO. These receipts establish
-- identity and automatic retention, not snapshot completion or a writer barrier.
CREATE TABLE project_environment_clone_postgres_snapshots (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
    source_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE RESTRICT,
    source_version text NOT NULL CHECK (source_version ~ '^[a-f0-9]{64}$'),
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    source_provider_resource_id text NOT NULL CHECK (source_provider_resource_id<>'' AND length(source_provider_resource_id)<=255),
    source_data_resource_id text NOT NULL CHECK (source_data_resource_id<>'' AND length(source_data_resource_id)<=255),
    capture_point timestamptz NOT NULL,
    provider_snapshot_id text CHECK (provider_snapshot_id IS NULL OR (provider_snapshot_id<>'' AND length(provider_snapshot_id)<=255)),
    state text NOT NULL DEFAULT 'capturing' CHECK (state IN ('capturing','retained','deleting','deleted')),
    snapshot_created_at timestamptz,
    observed_at timestamptz,
    cleanup_observed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (operation_id,source_database_id),
    UNIQUE (backend_id,backend_fingerprint,provider_snapshot_id),
    CHECK (capture_point<=created_at),
    CHECK ((provider_snapshot_id IS NULL AND snapshot_created_at IS NULL AND observed_at IS NULL)
        OR (provider_snapshot_id IS NOT NULL AND snapshot_created_at IS NOT NULL AND snapshot_created_at>=capture_point AND observed_at IS NOT NULL)),
    CHECK (state<>'retained' OR provider_snapshot_id IS NOT NULL),
    CHECK ((state='deleted')=(cleanup_observed_at IS NOT NULL))
);
CREATE INDEX project_environment_clone_postgres_snapshot_source_hold
    ON project_environment_clone_postgres_snapshots(source_database_id) WHERE state<>'deleted';

-- +goose Down
DROP TABLE project_environment_clone_postgres_snapshots;
