-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE database_nodes ADD CONSTRAINT database_nodes_archive_fleet_key UNIQUE USING INDEX database_nodes_archive_fleet_key;

ALTER TABLE database_vms
    ADD CONSTRAINT database_vms_move_archive_fleet_fk FOREIGN KEY (move_node, recovery_region)
        REFERENCES database_nodes (id, recovery_region) NOT VALID,
    ADD CONSTRAINT database_vms_primary_archive_fleet_fk FOREIGN KEY (node_id, recovery_region)
        REFERENCES database_nodes (id, recovery_region) NOT VALID,
    ADD CONSTRAINT database_vms_standby_archive_fleet_fk FOREIGN KEY (standby_node, recovery_region)
        REFERENCES database_nodes (id, recovery_region) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE database_vms
    DROP CONSTRAINT database_vms_move_archive_fleet_fk,
    DROP CONSTRAINT database_vms_primary_archive_fleet_fk,
    DROP CONSTRAINT database_vms_standby_archive_fleet_fk;
ALTER TABLE database_nodes DROP CONSTRAINT database_nodes_archive_fleet_key;
