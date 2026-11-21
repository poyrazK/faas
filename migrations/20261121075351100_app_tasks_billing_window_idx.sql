-- filename: 20261121075351100_app_tasks_billing_window_idx.sql
-- migration-compat: expand
-- Cost-control review 2026-11-19 (N16): meterd bills app task VMs from their
-- started_at/finished_at window each minute. This index finds the tasks that
-- started within a task's longest run before a sampled minute. Built
-- concurrently so app task writes never wait.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'app_tasks_billing_window_idx' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX app_tasks_billing_window_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS app_tasks_billing_window_idx
    ON app_tasks (started_at) WHERE started_at IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS app_tasks_billing_window_idx;
