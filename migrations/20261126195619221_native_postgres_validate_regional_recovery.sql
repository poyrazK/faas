-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_nodes VALIDATE CONSTRAINT database_nodes_recovery_region_check;
ALTER TABLE database_vms
    VALIDATE CONSTRAINT database_vms_recovery_region_check,
    VALIDATE CONSTRAINT database_vms_restore_from_recovery_check,
    VALIDATE CONSTRAINT database_vms_move_archive_fleet_fk,
    VALIDATE CONSTRAINT database_vms_primary_archive_fleet_fk,
    VALIDATE CONSTRAINT database_vms_standby_archive_fleet_fk;
ALTER TABLE managed_postgres_databases
    VALIDATE CONSTRAINT managed_postgres_recovery_region_check,
    VALIDATE CONSTRAINT managed_postgres_restore_from_recovery_check;

-- +goose Down
SELECT 1;
