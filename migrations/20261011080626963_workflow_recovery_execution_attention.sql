-- +goose Up
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'event_recovery.execution_attention');
END $$;
-- +goose StatementEnd
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_jobs_notification_receipts_chk;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_notification_receipts_chk CHECK (jsonb_typeof(notification_receipts)='object' AND notification_receipts-ARRAY['event_recovery.completed','event_recovery.cancelled','event_recovery.expired','event_recovery.execution_finished','event_recovery.execution_attention']='{}'::jsonb);
ALTER TABLE event_recovery_jobs ADD COLUMN execution_attention_next_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX event_recovery_execution_attention_pending_idx ON event_recovery_jobs(execution_attention_next_at,id)
 WHERE selection->>'mode'='workflow' AND execution_finished_at IS NULL
 AND NOT notification_receipts ? 'event_recovery.execution_attention';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE notification_receipts ? 'event_recovery.execution_attention') OR EXISTS (SELECT 1 FROM app_webhook_event_outbox WHERE event='event_recovery.execution_attention') THEN
  RAISE EXCEPTION 'cannot downgrade while execution attention receipts are retained';
 END IF;
END $$;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_jobs_notification_receipts_chk;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_jobs_notification_receipts_chk CHECK (jsonb_typeof(notification_receipts)='object' AND notification_receipts-ARRAY['event_recovery.completed','event_recovery.cancelled','event_recovery.expired','event_recovery.execution_finished']='{}'::jsonb);
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (((event = ANY (ARRAY['usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.escalated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text, 'app.health.changed'::text, 'event_recovery.completed'::text, 'event_recovery.cancelled'::text, 'event_recovery.expired'::text, 'event_recovery.execution_finished'::text, 'profile.route_regressed'::text, 'profile.route_recovered'::text])) OR (event = 'automation.paused'::text)));
DROP INDEX event_recovery_execution_attention_pending_idx;
ALTER TABLE event_recovery_jobs DROP COLUMN execution_attention_next_at;
-- +goose StatementEnd
