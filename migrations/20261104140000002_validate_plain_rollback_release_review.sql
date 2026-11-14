-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments VALIDATE CONSTRAINT deployments_rollback_release_review_check;

-- +goose Down
SELECT 1;
