-- filename: 20261017070545475_ci_automation_event_recovery.sql

-- +goose Up
-- Recovery resets admission only; original source and execution identity survive.
ALTER TABLE ci_automation_events ADD COLUMN IF NOT EXISTS retry_generation integer NOT NULL DEFAULT 0
 CHECK(retry_generation BETWEEN 0 AND 3);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_automation_events'::regclass AND conname='ci_automation_events_account_id_id_key') THEN
  ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_account_id_id_key UNIQUE(account_id,id);
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS ci_automation_event_retries (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 event_id uuid NOT NULL,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 3),
 key_digest text NOT NULL CHECK(key_digest ~ '^[0-9a-f]{64}$'),
 previous_attempts integer NOT NULL CHECK(previous_attempts=8),
 previous_error text NOT NULL CHECK(previous_error IN ('unavailable','capacity')),
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(account_id,event_id) REFERENCES ci_automation_events(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,key_digest),
 UNIQUE(event_id,generation)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_automation_event_retries) THEN
  RAISE EXCEPTION 'retain the CI automation recovery audit and lifetime retry budget before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_automation_event_retries;
ALTER TABLE ci_automation_events DROP CONSTRAINT ci_automation_events_account_id_id_key;
ALTER TABLE ci_automation_events DROP COLUMN retry_generation;
