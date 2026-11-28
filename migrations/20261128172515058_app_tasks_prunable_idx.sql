-- filename: 20261128172515058_app_tasks_prunable_idx.sql
-- migration-compat: expand
-- schedd deletes finished manual and cron app tasks past their plan's
-- retention (Limits.AppTaskRetentionDays), oldest first. Without an index on
-- finished_at each pass would scan every task ever run;
-- app_tasks_prunable_idx holds only the finished tasks the sweep may delete.
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
            WHERE c.relname = 'app_tasks_prunable_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX app_tasks_prunable_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS app_tasks_prunable_idx
    ON app_tasks (finished_at)
    WHERE kind IN ('manual', 'cron') AND binding_verification IS NULL AND finished_at IS NOT NULL;

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS app_tasks_prunable_idx;
RESET lock_timeout;
