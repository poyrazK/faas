-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhooks DROP CONSTRAINT app_webhooks_scope_chk;
-- +goose Down
SELECT 1;
