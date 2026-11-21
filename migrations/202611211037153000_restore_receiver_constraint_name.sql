-- migration-compat: contract
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhooks RENAME CONSTRAINT app_webhooks_run_scope_chk TO app_webhooks_scope_chk;
-- +goose Down
SELECT 1;
