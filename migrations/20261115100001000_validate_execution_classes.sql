-- filename: 20261115100001000_validate_execution_classes.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE executions VALIDATE CONSTRAINT executions_class_timeout_ms_check;
-- +goose Down
SELECT 1;
