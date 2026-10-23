-- filename: 20261022181558448_profile_capture_webhook_events_validate.sql
-- ADR-1155: second of three steps widening the app webhook outbox event check
-- for profile.capture_ready and profile.capture_oom_linked: validates the
-- wider check without blocking writes. Nothing to do when the first step
-- found both events already allowed.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass
            AND conname='app_webhook_event_outbox_event_capture_chk') THEN
  ALTER TABLE app_webhook_event_outbox VALIDATE CONSTRAINT app_webhook_event_outbox_event_capture_chk;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Validation leaves nothing to undo.
SELECT 1;
