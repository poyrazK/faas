-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE jobs VALIDATE CONSTRAINT jobs_environment_git_source_fk;

-- +goose Down
SELECT 1;
