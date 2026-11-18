-- filename: 20261117131442100_sandbox_action_screenshots_idx.sql
-- migration-compat: expand
-- ADR-20261117065818 §5: agentd's screenshot sweep finds a session's kept
-- screenshots through this index. Built concurrently so sandbox_actions
-- writes never wait; the table is new and small, so no timeout bound.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'sandbox_actions_screenshots_idx' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX sandbox_actions_screenshots_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS sandbox_actions_screenshots_idx
    ON sandbox_actions (session_id) WHERE screenshot_key IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS sandbox_actions_screenshots_idx;
