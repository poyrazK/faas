-- +goose Up
ALTER TABLE workflow_recovery_policies ADD COLUMN IF NOT EXISTS automatic_poll_notification jsonb CHECK(automatic_poll_notification IS NULL OR (
 jsonb_typeof(automatic_poll_notification)='object'
 AND automatic_poll_notification->>'status' IN ('queued','no_receivers')
 AND automatic_poll_notification->'payload'->>'state' IN ('failed','recovered')
 AND automatic_poll_notification->'payload'->>'scope'='automatic_policy'
 AND (automatic_poll_notification->'payload'->>'sequence')::bigint>0
 AND jsonb_typeof(automatic_poll_notification->'receiver_webhook_ids')='array'
 AND isfinite((automatic_poll_notification->>'captured_at')::timestamptz)
) IS TRUE);
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF position('automation.recovery_polling_status' in definition)>0 THEN RETURN; END IF;
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_polling_status');
END $$;
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='workflow_recovery_history_events'::regclass AND conname='workflow_recovery_history_events_check';
 IF position('polling_failed' in definition)>0 THEN RETURN; END IF;
 ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_events_check;
 definition:=replace(definition,'''initial_recovery_result''::text', '''initial_recovery_result''::text, ''polling_failed''::text, ''polling_recovered''::text');
 EXECUTE 'ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_events_check '||definition;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policies WHERE automatic_poll_notification IS NOT NULL)
 OR EXISTS(SELECT 1 FROM workflow_initial_recovery_reviews WHERE review ? 'polling_notification')
 OR EXISTS(SELECT 1 FROM workflow_recovery_history_events WHERE entry->>'kind' IN ('polling_failed','polling_recovered'))
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event='automation.recovery_polling_status')
 OR EXISTS(SELECT 1 FROM app_webhook_deliveries WHERE event='automation.recovery_polling_status') THEN RAISE EXCEPTION 'polling notification evidence is retained'; END IF;
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event<>%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_polling_status');
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='workflow_recovery_history_events'::regclass AND conname='workflow_recovery_history_events_check';
 ALTER TABLE workflow_recovery_history_events DROP CONSTRAINT workflow_recovery_history_events_check;
 definition:=replace(definition,', ''polling_failed''::text, ''polling_recovered''::text', '');
 EXECUTE 'ALTER TABLE workflow_recovery_history_events ADD CONSTRAINT workflow_recovery_history_events_check '||definition;
END $$;
-- +goose StatementEnd
ALTER TABLE workflow_recovery_policies DROP COLUMN automatic_poll_notification;
