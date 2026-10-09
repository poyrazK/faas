-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_webhook_event_outbox
    DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk') THEN
        ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((event = ANY (ARRAY['usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'app.health.changed'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.escalated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text,'event_recovery.completed'::text,'event_recovery.cancelled'::text,'event_recovery.expired'::text])));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Preserve the event vocabulary so already committed notifications survive binary rollback.
SELECT 1;
