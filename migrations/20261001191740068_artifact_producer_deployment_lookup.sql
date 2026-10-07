-- filename: 20261001191740068_artifact_producer_deployment_lookup.sql

-- +goose Up
-- adr: 393. Complete evidence reads must retain any producer lineage, even
-- when the current selection is stale. Bound the per-deployment lookup.
CREATE INDEX IF NOT EXISTS deployment_registry_rootfs_deployment_idx
 ON deployment_registry_rootfs (deployment_id);

-- +goose Down
DROP INDEX IF EXISTS deployment_registry_rootfs_deployment_idx;
