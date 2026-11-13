-- migration-compat: expand
-- filename: 20261031102736660_queue_completed_payload_trim_index.sql

-- +goose NO TRANSACTION

-- +goose Up
-- ADR-20261018101500 amendment 2026-10-31: a completed queue message keeps its
-- payload for a day after its ack, then only its history. The retention sweep
-- reads this index in completion order; a trimmed row leaves it. The build
-- runs without the migrator's statement timeout so a large invocations table
-- is not cancelled halfway.
SET statement_timeout = 0;
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'invocations_queue_completed_payload_idx' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX invocations_queue_completed_payload_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_queue_completed_payload_idx
    ON invocations (completed_at)
    WHERE source = 'queue' AND state = 'completed' AND payload <> 'null'::jsonb;
RESET lock_timeout;
RESET statement_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS invocations_queue_completed_payload_idx;
RESET lock_timeout;
