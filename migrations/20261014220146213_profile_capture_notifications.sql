-- filename: 20261014220146213_profile_capture_notifications.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Automatic profile capture notifications (ADR-1155): schedd enqueues
-- profile.capture_ready and profile.capture_oom_linked into the app webhook
-- outbox in the transaction that finishes a capture or links an OOM kill.
-- +goose StatementBegin
DO $$
DECLARE definition text; added text;
BEGIN
 FOREACH added IN ARRAY ARRAY['profile.capture_ready', 'profile.capture_oom_linked'] LOOP
  SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
  WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
  IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
  CONTINUE WHEN position(quote_literal(added) in definition)>0;
  definition:=regexp_replace(definition,' NOT VALID$','');
  ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
  EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),added);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Retain event vocabulary for queued deliveries.
SELECT 1;
