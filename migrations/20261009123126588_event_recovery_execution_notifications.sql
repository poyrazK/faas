-- +goose Up
-- +goose StatementBegin
-- Existing jobs do not generate retrospective execution notifications.
ALTER TABLE event_recovery_jobs
 ADD COLUMN execution_notification_captured boolean NOT NULL DEFAULT true,
 ADD COLUMN execution_finished_at timestamptz,
 ADD COLUMN execution_notification_next_at timestamptz NOT NULL DEFAULT now(),
 ADD CONSTRAINT event_recovery_execution_finished_chk CHECK
  (execution_finished_at IS NULL OR (execution_notification_captured AND completed_at IS NOT NULL AND execution_finished_at>=completed_at));
ALTER TABLE event_recovery_jobs ALTER COLUMN execution_notification_captured SET DEFAULT false;
CREATE INDEX event_recovery_execution_notification_pending_idx ON event_recovery_jobs(execution_notification_next_at,id)
 WHERE NOT execution_notification_captured AND state IN ('completed','cancelled') AND selection->>'mode'='execution';
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((event = ANY (ARRAY['usage_statement.finalized'::text, 'app.parked'::text, 'app.woken'::text, 'issue.created'::text, 'issue.assigned'::text, 'issue.resolved'::text, 'issue.reopened'::text, 'issue.ignored'::text, 'issue.regressed'::text, 'issue.impact_threshold_reached'::text, 'routes.requirements.violated'::text, 'routes.requirements.recovered'::text, 'routes.requirements.changed'::text, 'routes.health.blocked'::text, 'routes.health.resumed'::text, 'routes.health.aborted'::text, 'routes.monitor.violated'::text, 'routes.monitor.escalated'::text, 'routes.monitor.recovered'::text, 'workflow.finished'::text, 'app.health.changed'::text, 'event_recovery.completed'::text, 'event_recovery.cancelled'::text, 'event_recovery.expired'::text, 'event_recovery.execution_finished'::text, 'profile.route_regressed'::text, 'profile.route_recovered'::text])));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS event_recovery_execution_notification_pending_idx;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT IF EXISTS event_recovery_execution_finished_chk,
 DROP COLUMN IF EXISTS execution_finished_at, DROP COLUMN IF EXISTS execution_notification_captured,
 DROP COLUMN IF EXISTS execution_notification_next_at;
-- Keep expanded outbox vocabulary so committed notifications survive rollback.
-- +goose StatementEnd
