-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_run_scope_chk;
-- +goose Down
SELECT 1;
