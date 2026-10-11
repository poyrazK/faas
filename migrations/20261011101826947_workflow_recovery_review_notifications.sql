-- +goose Up
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_review_ready');
END $$;
-- +goose StatementEnd
CREATE TABLE workflow_recovery_review_notifications (
 review_id uuid PRIMARY KEY REFERENCES workflow_recovery_policy_reviews(id) ON DELETE CASCADE,
 next_at timestamptz NOT NULL DEFAULT now(),
 receipt jsonb CHECK(receipt IS NULL OR (jsonb_typeof(receipt)='object' AND receipt->>'status' IN ('queued','no_receivers','suppressed') AND receipt ?& ARRAY['event_id','captured_at','receiver_webhook_ids','reason']))
);
CREATE INDEX workflow_recovery_review_notifications_pending ON workflow_recovery_review_notifications(next_at,review_id) WHERE receipt IS NULL;
INSERT INTO workflow_recovery_review_notifications(review_id) SELECT id FROM workflow_recovery_policy_reviews WHERE review ? 'plan';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_review_notifications WHERE receipt IS NOT NULL)
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event='automation.recovery_review_ready')
 OR EXISTS(SELECT 1 FROM app_webhook_deliveries WHERE event='automation.recovery_review_ready') THEN
 RAISE EXCEPTION 'cannot downgrade while review notification evidence is retained'; END IF;
END $$;
DROP TABLE workflow_recovery_review_notifications;
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 -- Keeping the existing closed vocabulary plus excluding this event is equivalent
 -- to the prior vocabulary, without depending on pg_get_constraintdef formatting.
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event<>%L)',substring(definition from 8 for length(definition)-8),'automation.recovery_review_ready');
END $$;
-- +goose StatementEnd
