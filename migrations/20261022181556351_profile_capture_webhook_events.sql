-- filename: 20261022181556351_profile_capture_webhook_events.sql
-- ADR-1155: schedd enqueues profile.capture_ready and
-- profile.capture_oom_linked into the app webhook outbox. The outbox event
-- check is widened online in three steps, like 20261021183000601..603: this
-- one adds the wider check NOT VALID under a new name, so new rows are checked
-- at once. A check that already allows both events is left alone, and the
-- next two steps then do nothing.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
DECLARE definition text; body text; wider text; added text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 body:=substring(definition from 8 for length(definition)-8);
 wider:=body;
 FOREACH added IN ARRAY ARRAY['profile.capture_ready', 'profile.capture_oom_linked'] LOOP
  CONTINUE WHEN position(quote_literal(added) in definition)>0;
  wider:=format('(%s) OR event=%L', wider, added);
 END LOOP;
 IF wider <> body THEN
  EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_capture_chk CHECK (%s) NOT VALID', wider);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_capture_chk;
