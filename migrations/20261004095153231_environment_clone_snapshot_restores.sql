-- +goose Up
-- ADR-531: native fork authority survives acknowledgement loss. Storage
-- completion does not authorize catalogue readiness or stage publication.
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_snapshot_restores (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    target_owner_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','requested','restoring','restored')),
    target_provider_resource_id text CHECK (target_provider_resource_id IS NULL OR (target_provider_resource_id<>'' AND length(target_provider_resource_id)<=255)),
    target_created_at timestamptz,
    request_started_at timestamptz,
    observed_at timestamptz,
    restored_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (operation_id,source_database_id),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_snapshots(operation_id,source_database_id) ON DELETE RESTRICT,
    UNIQUE (backend_id,backend_fingerprint,target_provider_resource_id),
    CHECK (target_owner_id<>source_database_id),
    CHECK ((state='reserved' AND request_started_at IS NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL)
        OR (state='requested' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NULL AND target_created_at IS NULL AND observed_at IS NULL AND restored_at IS NULL)
        OR (state='restoring' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NULL)
        OR (state='restored' AND request_started_at IS NOT NULL AND target_provider_resource_id IS NOT NULL AND target_created_at IS NOT NULL AND observed_at IS NOT NULL AND restored_at IS NOT NULL)),
    CHECK (target_created_at IS NULL OR target_created_at<=observed_at)
);

-- +goose Down
DROP TABLE project_environment_clone_postgres_snapshot_restores;
