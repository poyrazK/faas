-- +goose Up
-- ADR-531: retain observed provider lineage with clone-owned readiness.
-- Only the target owns this receipt's lifetime. Retain the original source,
-- account and operation identities without extra parent locks during the
-- atomic readiness write; readers authenticate them against the target row.
CREATE TABLE managed_postgres_restore_proofs (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id uuid NOT NULL CHECK (account_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    operation_id uuid NOT NULL CHECK (operation_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    provider_resource_id text NOT NULL CHECK (provider_resource_id<>'' AND length(provider_resource_id)<=255),
    source_database_id uuid NOT NULL CHECK (source_database_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    source_resource_id text NOT NULL CHECK (source_resource_id<>'' AND length(source_resource_id)<=255),
    point_in_time timestamptz NOT NULL CHECK (isfinite(point_in_time)),
    spec jsonb NOT NULL CHECK (jsonb_typeof(spec)='object' AND spec<>'{}'::jsonb),
    generation bigint NOT NULL CHECK (generation>0),
    observed_at timestamptz NOT NULL CHECK (isfinite(observed_at)),
    CHECK (database_id<>source_database_id AND provider_resource_id<>source_resource_id)
);

-- +goose Down
DROP TABLE managed_postgres_restore_proofs;
