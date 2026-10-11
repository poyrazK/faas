-- +goose Up
ALTER TABLE workflow_recovery_policy_executions
 ADD COLUMN suppression jsonb CHECK(suppression IS NULL OR (jsonb_typeof(suppression)='object' AND status='suppressed' AND reason='operator_suppressed')),
 ADD COLUMN blocked_notification jsonb CHECK(blocked_notification IS NULL OR (jsonb_typeof(blocked_notification)='object' AND blocked_notification->>'status' IN ('queued','no_receivers'))),
 ADD COLUMN next_blocked_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX workflow_recovery_policy_executions_blocked_idx ON workflow_recovery_policy_executions(next_blocked_at,app_id,action_id) WHERE blocked_notification IS NULL;
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_queue_blocked');
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_executions WHERE suppression IS NOT NULL OR blocked_notification IS NOT NULL)
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event='automation.recovery_queue_blocked')
 OR EXISTS(SELECT 1 FROM app_webhook_deliveries WHERE event='automation.recovery_queue_blocked') THEN
 RAISE EXCEPTION 'cannot discard automatic recovery suppression or blocked notification evidence';END IF;
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event<>%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_queue_blocked');
END $$;
-- +goose StatementEnd
DROP INDEX workflow_recovery_policy_executions_blocked_idx;
ALTER TABLE workflow_recovery_policy_executions DROP COLUMN suppression,DROP COLUMN blocked_notification,DROP COLUMN next_blocked_at;
