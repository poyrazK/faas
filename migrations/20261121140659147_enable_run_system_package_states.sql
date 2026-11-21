-- migration-compat: contract
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_environments DROP CONSTRAINT IF EXISTS execution_environments_status_check;
-- +goose Down
SELECT 1;
