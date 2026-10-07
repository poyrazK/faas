-- +goose Up
-- ADR-638: custody of accepted creations is separate from data correctness.
CREATE TABLE managed_postgres_creation_receipts (
    kind text NOT NULL CHECK (kind IN ('restore','snapshot')),
    resource_id text NOT NULL CHECK (resource_id<>'' AND length(resource_id)<=255),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    database_id uuid REFERENCES managed_postgres_databases(id) ON DELETE RESTRICT,
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    generation bigint NOT NULL CHECK (generation>0),
    point_in_time timestamptz NOT NULL CHECK (isfinite(point_in_time)),
    source_resource_id text NOT NULL CHECK (source_resource_id<>'' AND length(source_resource_id)<=255),
    provider_resource_id text NOT NULL CHECK (provider_resource_id<>'' AND length(provider_resource_id)<=255),
    provider_created_at timestamptz NOT NULL CHECK (isfinite(provider_created_at)),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    cleanup_started_at timestamptz CHECK (cleanup_started_at IS NULL OR isfinite(cleanup_started_at)),
    PRIMARY KEY (kind,backend_id,resource_id),
    UNIQUE (backend_id,backend_fingerprint,provider_resource_id),
    CHECK (provider_resource_id<>source_resource_id AND point_in_time<=provider_created_at AND provider_created_at<=recorded_at),
    CHECK ((kind='restore' AND database_id IS NOT NULL AND resource_id=database_id::text) OR (kind='snapshot' AND database_id IS NULL))
);

-- +goose Down
DROP TABLE managed_postgres_creation_receipts;
