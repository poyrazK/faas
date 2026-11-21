-- migration-compat: contract
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_webhooks'::regclass AND conname = 'app_webhooks_run_budget_scope_chk') THEN
  ALTER TABLE app_webhooks DROP CONSTRAINT IF EXISTS app_webhooks_scope_chk;
  ALTER TABLE app_webhooks RENAME CONSTRAINT app_webhooks_run_budget_scope_chk TO app_webhooks_scope_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
