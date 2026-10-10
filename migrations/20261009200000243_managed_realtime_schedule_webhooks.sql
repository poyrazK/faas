-- +goose Up
-- Preserve the vocabulary installed by earlier producers.
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L,%L))',substring(definition from 8 for length(definition)-8),'realtime.schedule.published','realtime.schedule.failed','realtime.schedule.skipped');
END $$;
-- +goose StatementEnd
-- +goose Down
-- Keep the vocabulary so queued deliveries remain dispatchable and replayable.
SELECT 1;
