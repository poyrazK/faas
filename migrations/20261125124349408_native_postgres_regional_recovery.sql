-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_nodes
    ADD COLUMN recovery_region text NOT NULL DEFAULT '',
    ADD CONSTRAINT database_nodes_recovery_region_check
        CHECK (recovery_region = '' OR recovery_region ~ '^[a-z0-9][a-z0-9-]{0,62}$') NOT VALID;

ALTER TABLE database_vms
    ADD COLUMN recovery_region text NOT NULL DEFAULT '',
    ADD COLUMN restore_from_recovery boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT database_vms_recovery_region_check
        CHECK (recovery_region = '' OR recovery_region ~ '^[a-z0-9][a-z0-9-]{0,62}$') NOT VALID,
    ADD CONSTRAINT database_vms_restore_from_recovery_check
        CHECK (NOT restore_from_recovery OR (recovery_region <> '' AND restore_source IS NOT NULL)) NOT VALID;

ALTER TABLE managed_postgres_databases
    ADD COLUMN recovery_region text NOT NULL DEFAULT '',
    ADD COLUMN restore_from_recovery boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT managed_postgres_recovery_region_check
        CHECK (recovery_region = '' OR (recovery_region <> region AND recovery_region ~ '^[a-z0-9][a-z0-9-]{0,62}$')) NOT VALID,
    ADD CONSTRAINT managed_postgres_restore_from_recovery_check
        CHECK (NOT restore_from_recovery OR (restore_source_resource_id IS NOT NULL AND restore_point_in_time IS NOT NULL)) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_databases DROP COLUMN restore_from_recovery, DROP COLUMN recovery_region;
ALTER TABLE database_vms DROP COLUMN restore_from_recovery, DROP COLUMN recovery_region;
ALTER TABLE database_nodes DROP COLUMN recovery_region;
