-- filename: 20261022181600910_profile_capture_webhook_events_swap.sql
-- ADR-1155: last of three steps widening the app webhook outbox event check
-- for profile.capture_ready and profile.capture_oom_linked: drops the old
-- check and gives the wider one its name, which later migrations widening
-- the vocabulary look up. Nothing to do when the first step found both
-- events already allowed.
-- migration-compat: contract

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass
            AND conname='app_webhook_event_outbox_event_capture_chk') THEN
  ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
  ALTER TABLE app_webhook_event_outbox RENAME CONSTRAINT app_webhook_event_outbox_event_capture_chk TO app_webhook_event_outbox_event_chk;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Retain the event vocabulary for queued deliveries.
SELECT 1;
