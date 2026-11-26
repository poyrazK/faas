-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_bindings VALIDATE CONSTRAINT managed_postgres_bindings_data_api_inspection_revision_check;
ALTER TABLE managed_postgres_databases VALIDATE CONSTRAINT managed_postgres_databases_data_api_inspection_revision_check;

-- +goose Down
-- Validation only strengthens checks already enforced for new writes.
SELECT 1;
