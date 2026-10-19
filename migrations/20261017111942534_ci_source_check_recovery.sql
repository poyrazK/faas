-- filename: 20261017111942534_ci_source_check_recovery.sql

-- +goose Up
ALTER TABLE ci_automation_path_checks ADD COLUMN IF NOT EXISTS retry_generation integer NOT NULL DEFAULT 0
 CHECK(retry_generation BETWEEN 0 AND 3);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_automation_path_checks'::regclass AND conname='ci_automation_path_checks_account_event_key') THEN
  ALTER TABLE ci_automation_path_checks ADD CONSTRAINT ci_automation_path_checks_account_event_key UNIQUE(account_id,event_id);
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS ci_source_check_retries (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 event_id uuid NOT NULL,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 3),
 key_digest text NOT NULL CHECK(key_digest ~ '^[0-9a-f]{64}$'),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 applied_at timestamptz,
 FOREIGN KEY(account_id,event_id) REFERENCES ci_automation_path_checks(account_id,event_id) ON DELETE CASCADE,
 UNIQUE(account_id,key_digest),
 UNIQUE(event_id,generation),
 CHECK(applied_at IS NULL OR applied_at>=requested_at)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_source_check_retries) OR EXISTS(SELECT 1 FROM ci_automation_path_checks WHERE retry_generation>0) THEN
  RAISE EXCEPTION 'retain CI source Check retry audit and idempotency evidence before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_source_check_retries;
ALTER TABLE ci_automation_path_checks DROP CONSTRAINT ci_automation_path_checks_account_event_key;
ALTER TABLE ci_automation_path_checks DROP COLUMN retry_generation;
