-- filename: 20261103093240897_issue_payload_capacity.sql
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
-- Input remains capped at 64 KiB. Normalization, parsed frames, and JSONB's
-- textual formatting can expand retained evidence, especially after mapping.
SET lock_timeout = '5s';
-- Install the wider check before removing the old one. Validate without
-- holding the table's exclusive DDL lock throughout the scan.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='issue_events'::regclass AND conname='issue_events_payload_v2_check') THEN
  ALTER TABLE issue_events ADD CONSTRAINT issue_events_payload_v2_check
   CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 1048576) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE issue_events VALIDATE CONSTRAINT issue_events_payload_v2_check;
ALTER TABLE issue_events DROP CONSTRAINT IF EXISTS issue_events_payload_check;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM issue_events WHERE octet_length(payload::text) > 65536) THEN
  RAISE EXCEPTION 'issue evidence exceeds the previous size limit; rollback would reject retained events';
 END IF;
 ALTER TABLE issue_events ADD CONSTRAINT issue_events_payload_check
  CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 65536);
 ALTER TABLE issue_events DROP CONSTRAINT issue_events_payload_v2_check;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
