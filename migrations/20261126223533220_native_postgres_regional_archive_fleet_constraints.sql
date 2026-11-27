-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- PostgreSQL can reference the preceding non-partial unique index directly.
-- The index remains in place if this migration is reverted.

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_move_archive_fleet_fk') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_move_archive_fleet_fk FOREIGN KEY (move_node, recovery_region)
            REFERENCES database_nodes (id, recovery_region) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_primary_archive_fleet_fk') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_primary_archive_fleet_fk FOREIGN KEY (node_id, recovery_region)
            REFERENCES database_nodes (id, recovery_region) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass
        AND conname = 'database_vms_standby_archive_fleet_fk') THEN
        ALTER TABLE database_vms ADD CONSTRAINT database_vms_standby_archive_fleet_fk FOREIGN KEY (standby_node, recovery_region)
            REFERENCES database_nodes (id, recovery_region) NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_vms
    DROP CONSTRAINT database_vms_move_archive_fleet_fk,
    DROP CONSTRAINT database_vms_primary_archive_fleet_fk,
    DROP CONSTRAINT database_vms_standby_archive_fleet_fk;
