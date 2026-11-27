-- filename: 20261121140659110_retire_standard_execution_timeout_check.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- The replacement class-aware check was added and validated in separate
-- transactions before relaxing the legacy standard-only ceiling.
ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_timeout_ms_check;
-- +goose Down
-- Forward-only: retain receipts and the validated class-aware constraint.
SELECT 1;
