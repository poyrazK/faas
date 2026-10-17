-- +goose Up
-- Keep parent identity and foreign keys; only detailed Jobs history is compacted.
ALTER TABLE ci_executions ADD COLUMN IF NOT EXISTS archived_at timestamptz;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'ci_executions'::regclass AND conname = 'ci_execution_archive_valid') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_execution_archive_valid CHECK(archived_at IS NULL OR
   (mode='jobs' AND status IN ('succeeded','failed','cancelled') AND finished_at IS NOT NULL AND lease_until IS NULL
   AND octet_length(snapshot::text)<=1024));
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS ci_execution_archives (
 account_id uuid NOT NULL,
 execution_id uuid PRIMARY KEY,
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 archived_at timestamptz NOT NULL,
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_executions(account_id,id) ON DELETE CASCADE,
 CHECK(receipt->>'execution_id' IS NOT NULL AND receipt->>'execution_id'=execution_id::text)
);
CREATE INDEX IF NOT EXISTS ci_execution_archives_account_idx ON ci_execution_archives(account_id,archived_at DESC,execution_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_execution_archives) OR EXISTS(SELECT 1 FROM ci_executions WHERE archived_at IS NOT NULL) THEN
  RAISE EXCEPTION 'CI archived execution detail cannot be restored; retain archive receipts before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_execution_archives;
ALTER TABLE ci_executions DROP CONSTRAINT ci_execution_archive_valid;
ALTER TABLE ci_executions DROP COLUMN archived_at;
