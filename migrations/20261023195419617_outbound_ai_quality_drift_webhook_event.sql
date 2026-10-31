-- filename: 20261023195419617_outbound_ai_quality_drift_webhook_event.sql
-- ADR 20261021171712: drift alerts enqueue outbound.ai_quality.drift into the
-- app webhook outbox. 20261021171858895 allowed it, but on a fresh database
-- 20261021183000601..603 run after it and replace the check with a list
-- that lacks it. The check is widened online again in three steps, like
-- 20261022181556351..20261022181600910: this one adds the wider check NOT
-- VALID under a new name, so new rows are checked at once. A check that
-- already allows the event is left alone, and the next two steps then do
-- nothing.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$
DECLARE definition text; body text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position(quote_literal('outbound.ai_quality.drift') in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 body:=substring(definition from 8 for length(definition)-8);
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_drift_chk CHECK ((%s) OR event=%L) NOT VALID',
                body, 'outbound.ai_quality.drift');
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_drift_chk;
