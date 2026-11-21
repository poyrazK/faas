-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE execution_environment_diagnostics VALIDATE CONSTRAINT execution_environment_diagnostics_shape;
-- +goose Down
SELECT 1;
