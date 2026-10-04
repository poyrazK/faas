-- +goose Up
-- ADR-531: a lifecycle resource may select mutable data (e.g. a Neon project
-- default branch). Pin the observed data identity with readiness. Existing
-- rows have no evidence and are deliberately not backfilled from an alias.
ALTER TABLE managed_postgres_databases ADD COLUMN data_resource_id text
    CHECK (data_resource_id IS NULL OR
        (data_resource_id<>'' AND length(data_resource_id)<=255 AND provider_resource_id IS NOT NULL));
ALTER TABLE managed_postgres_restore_proofs ADD COLUMN data_resource_id text
    CHECK (data_resource_id IS NULL OR
        (data_resource_id<>'' AND length(data_resource_id)<=255 AND data_resource_id<>source_resource_id));

-- +goose Down
ALTER TABLE managed_postgres_restore_proofs DROP COLUMN data_resource_id;
ALTER TABLE managed_postgres_databases DROP COLUMN data_resource_id;
