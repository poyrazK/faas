-- filename: 20261121140659107_validate_execution_file_budgets.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions VALIDATE CONSTRAINT executions_file_budget_check;
-- +goose Down
SELECT 1;
