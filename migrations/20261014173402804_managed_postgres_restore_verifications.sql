-- +goose Up
-- ADR-1259: the first independent verification of a restore target's lineage.
-- Later observations confirm its immutable-metadata evidence instead of
-- remapping a historical position the provider resolves inexactly. A record
-- lives exactly as long as its database row: final account erasure removes
-- deleted tombstones, and their verifications go with them.
CREATE TABLE IF NOT EXISTS managed_postgres_restore_verifications (
    database_id uuid PRIMARY KEY REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    backend_id text NOT NULL CHECK (backend_id<>'' AND length(backend_id)<=255),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    generation bigint NOT NULL CHECK (generation>0),
    provider_resource_id text NOT NULL CHECK (provider_resource_id<>'' AND length(provider_resource_id)<=255),
    source_resource_id text NOT NULL CHECK (source_resource_id<>'' AND length(source_resource_id)<=255),
    point_in_time timestamptz NOT NULL CHECK (isfinite(point_in_time)),
    evidence text NOT NULL CHECK (evidence<>'' AND length(evidence)<=1024),
    verified_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(verified_at)),
    UNIQUE (backend_id,backend_fingerprint,provider_resource_id),
    CHECK (provider_resource_id<>source_resource_id AND point_in_time<=verified_at)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    -- Serialize with verification writers before inspecting whether any exist.
    LOCK TABLE managed_postgres_restore_verifications IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM managed_postgres_restore_verifications) THEN
        RAISE EXCEPTION 'cannot remove retained managed postgres restore verifications'
            USING ERRCODE='23514', CONSTRAINT='managed_postgres_restore_verifications_retained';
    END IF;
END $$;
DROP TABLE managed_postgres_restore_verifications;
-- +goose StatementEnd
