-- +goose Up
-- Retain the reservation even after deletion: retrying the same operation must
-- never silently create a replacement physical database.
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_clone_reservation_key
    ON managed_postgres_databases(environment_clone_operation_id, restore_source_database_id)
    WHERE environment_clone_operation_id IS NOT NULL;

-- +goose Down
DROP INDEX managed_postgres_clone_reservation_key;
