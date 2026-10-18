-- +goose Up
-- apid admits immutable retry intent; githubd applies it to delivery bookkeeping.
ALTER TABLE ci_execution_checks ADD COLUMN IF NOT EXISTS retry_generation integer NOT NULL DEFAULT 0
 CHECK(retry_generation BETWEEN 0 AND 3);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'ci_execution_checks'::regclass AND conname = 'ci_execution_checks_account_execution_key') THEN
  ALTER TABLE ci_execution_checks ADD CONSTRAINT ci_execution_checks_account_execution_key UNIQUE(account_id,execution_id);
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS ci_execution_check_retries (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 execution_id uuid NOT NULL,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 3),
 key_digest text NOT NULL CHECK(key_digest ~ '^[0-9a-f]{64}$'),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 applied_at timestamptz,
 FOREIGN KEY(account_id,execution_id) REFERENCES ci_execution_checks(account_id,execution_id) ON DELETE CASCADE,
 UNIQUE(account_id,key_digest),
 UNIQUE(execution_id,generation),
 CHECK(applied_at IS NULL OR applied_at>=requested_at)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_execution_check_retries) THEN
  RAISE EXCEPTION 'retain the CI Check retry audit and idempotency journal before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_execution_check_retries;
ALTER TABLE ci_execution_checks DROP CONSTRAINT ci_execution_checks_account_execution_key;
ALTER TABLE ci_execution_checks DROP COLUMN retry_generation;
