-- +goose Up
-- ADR-531: a private maintenance owner persists across clone worker/operation
-- turnover. The operation-owned source fence must precede this reservation.
CREATE TABLE IF NOT EXISTS managed_postgres_checkpoint_maintenance (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_database_id uuid NOT NULL UNIQUE REFERENCES managed_postgres_databases(id) ON DELETE RESTRICT,
    reserved_by_operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    source_provider_resource_id text NOT NULL CHECK (source_provider_resource_id<>'' AND length(source_provider_resource_id)<=255),
    source_data_resource_id text NOT NULL CHECK (source_data_resource_id<>'' AND length(source_data_resource_id)<=255),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','role_requested','role_reserved','database_requested','database_created','activation_requested','ready')),
    owner_oid bigint CHECK (owner_oid>0 AND owner_oid<=4294967295),
    database_oid bigint CHECK (database_oid>0 AND database_oid<=4294967295),
    role_requested_at timestamptz,
    database_requested_at timestamptz,
    activation_requested_at timestamptz,
    ready_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (backend_id,backend_fingerprint,source_data_resource_id),
    CHECK (
        (state='reserved' AND role_requested_at IS NULL AND owner_oid IS NULL AND database_requested_at IS NULL AND database_oid IS NULL AND activation_requested_at IS NULL AND ready_at IS NULL) OR
        (state='role_requested' AND role_requested_at IS NOT NULL AND owner_oid IS NULL AND database_requested_at IS NULL AND database_oid IS NULL AND activation_requested_at IS NULL AND ready_at IS NULL) OR
        (state='role_reserved' AND role_requested_at IS NOT NULL AND owner_oid IS NOT NULL AND database_requested_at IS NULL AND database_oid IS NULL AND activation_requested_at IS NULL AND ready_at IS NULL) OR
        (state='database_requested' AND role_requested_at IS NOT NULL AND owner_oid IS NOT NULL AND database_requested_at IS NOT NULL AND database_oid IS NULL AND activation_requested_at IS NULL AND ready_at IS NULL) OR
        (state='database_created' AND role_requested_at IS NOT NULL AND owner_oid IS NOT NULL AND database_requested_at IS NOT NULL AND database_oid IS NOT NULL AND activation_requested_at IS NULL AND ready_at IS NULL) OR
        (state='activation_requested' AND role_requested_at IS NOT NULL AND owner_oid IS NOT NULL AND database_requested_at IS NOT NULL AND database_oid IS NOT NULL AND activation_requested_at IS NOT NULL AND ready_at IS NULL) OR
        (state='ready' AND role_requested_at IS NOT NULL AND owner_oid IS NOT NULL AND database_requested_at IS NOT NULL AND database_oid IS NOT NULL AND activation_requested_at IS NOT NULL AND ready_at IS NOT NULL)
    )
);

-- +goose Down
DROP TABLE managed_postgres_checkpoint_maintenance;
