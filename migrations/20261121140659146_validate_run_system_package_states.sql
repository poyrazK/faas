-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_environments VALIDATE CONSTRAINT execution_environments_system_status_check;
-- +goose Down
SELECT 1;
