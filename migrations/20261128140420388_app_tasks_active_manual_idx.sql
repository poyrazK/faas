-- filename: 20261128140420388_app_tasks_active_manual_idx.sql
-- migration-compat: expand
-- The app-task concurrency cap counts an account's manual tasks queued,
-- restoring or running each time one is created. app_tasks_account_app_created_idx
-- covers every task the account ever ran, so the count would walk its whole
-- history; app_tasks_account_active_manual_idx holds only the active manual
-- tasks, a handful per account.
-- Built concurrently so task admission and scheduling never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'app_tasks_account_active_manual_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX app_tasks_account_active_manual_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS app_tasks_account_active_manual_idx
    ON app_tasks (account_id)
    WHERE kind = 'manual' AND status IN ('queued', 'restoring', 'running');

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS app_tasks_account_active_manual_idx;
RESET lock_timeout;
