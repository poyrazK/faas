-- +goose Up
-- ADR-531: native capture forks can share production's provider project.
-- Keep their catalogue identities separate from the configured stage target.
ALTER TABLE managed_postgres_databases
    ADD COLUMN clone_resource_role text NOT NULL DEFAULT 'target'
        CHECK (clone_resource_role IN ('target','checkpoint')),
    ADD CHECK (clone_resource_role='target' OR environment_clone_operation_id IS NOT NULL);
DROP INDEX managed_postgres_clone_reservation_key;
CREATE UNIQUE INDEX managed_postgres_clone_reservation_key
    ON managed_postgres_databases(environment_clone_operation_id,restore_source_database_id)
    WHERE environment_clone_operation_id IS NOT NULL AND clone_resource_role='target';
CREATE UNIQUE INDEX managed_postgres_clone_capture_key
    ON managed_postgres_databases(environment_clone_operation_id,restore_source_database_id)
    WHERE environment_clone_operation_id IS NOT NULL AND clone_resource_role='checkpoint';

-- +goose Down
-- The old single-target key cannot represent capture ownership.
ALTER TABLE managed_postgres_databases
    ADD CONSTRAINT clone_capture_down_no_owned_captures CHECK (clone_resource_role='target');
DROP INDEX managed_postgres_clone_capture_key;
DROP INDEX managed_postgres_clone_reservation_key;
ALTER TABLE managed_postgres_databases
    DROP CONSTRAINT clone_capture_down_no_owned_captures,
    DROP COLUMN clone_resource_role;
CREATE UNIQUE INDEX managed_postgres_clone_reservation_key
    ON managed_postgres_databases(environment_clone_operation_id,restore_source_database_id)
    WHERE environment_clone_operation_id IS NOT NULL;
