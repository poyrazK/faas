-- filename: 20261104203215414_validate_project_shared_secret_generation.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_environments VALIDATE CONSTRAINT project_environments_shared_secret_generation_check;

-- +goose Down
-- Validation does not change the accepted values. The preceding migration
-- owns the constraint and column and removes them on rollback.
SELECT 1;
