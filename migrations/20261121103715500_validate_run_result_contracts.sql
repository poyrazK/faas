-- filename: 20261121103715500_validate_run_result_contracts.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions VALIDATE CONSTRAINT executions_result_validation_check;

-- +goose Down
SELECT 1;
