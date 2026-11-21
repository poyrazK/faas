-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_webhooks'::regclass AND conname = 'app_webhooks_pipeline_scope_chk') THEN
  ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_pipeline_scope_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
