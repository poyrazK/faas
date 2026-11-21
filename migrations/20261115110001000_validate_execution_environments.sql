-- filename: 20261115110001000_validate_execution_environments.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions VALIDATE CONSTRAINT executions_environment_id_fkey;
-- +goose Down
SELECT 1;
