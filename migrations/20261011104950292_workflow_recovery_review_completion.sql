-- +goose Up
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_review_finished');
END $$;
-- +goose StatementEnd
ALTER TABLE workflow_recovery_review_notifications ADD COLUMN next_completion_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE workflow_recovery_review_notifications ADD COLUMN completion jsonb CHECK(completion IS NULL OR (jsonb_typeof(completion)='object' AND completion->>'status' IN ('queued','no_receivers') AND completion ?& ARRAY['event_id','status','captured_at','receiver_webhook_ids','result'] AND jsonb_typeof(completion->'result')='object' AND ((completion->'result'->>'complete'='true') IS TRUE)));
CREATE INDEX workflow_recovery_review_completion_pending ON workflow_recovery_review_notifications(next_completion_at,review_id) WHERE completion IS NULL;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_review_notifications WHERE completion IS NOT NULL)
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event='automation.recovery_review_finished')
 OR EXISTS(SELECT 1 FROM app_webhook_deliveries WHERE event='automation.recovery_review_finished') THEN
 RAISE EXCEPTION 'cannot downgrade while review completion evidence is retained'; END IF;
END $$;
DROP INDEX workflow_recovery_review_completion_pending;
ALTER TABLE workflow_recovery_review_notifications DROP COLUMN completion;
ALTER TABLE workflow_recovery_review_notifications DROP COLUMN next_completion_at;
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event<>%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_review_finished');
END $$;
-- +goose StatementEnd
