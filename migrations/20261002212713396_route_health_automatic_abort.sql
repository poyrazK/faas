-- +goose Up
ALTER TABLE route_health_gates ADD COLUMN IF NOT EXISTS on_regression text NOT NULL DEFAULT 'hold'
    CHECK (on_regression IN ('hold', 'abort'));
ALTER TABLE route_health_notification_state DROP CONSTRAINT IF EXISTS route_health_notification_state_status_check;
ALTER TABLE route_health_notification_state ADD CONSTRAINT route_health_notification_state_status_check
    CHECK (status IN ('clear', 'blocked_unknown', 'blocked_regressed', 'aborted'));
ALTER TABLE route_health_notification_state DROP CONSTRAINT IF EXISTS route_health_notification_state_check;
ALTER TABLE route_health_notification_state ADD CONSTRAINT route_health_notification_state_check
    CHECK ((status IN ('clear', 'aborted') AND blocked_decision_id IS NULL) OR
           (status IN ('blocked_unknown', 'blocked_regressed') AND blocked_decision_id IS NOT NULL));
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
    CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed', 'routes.health.blocked', 'routes.health.resumed', 'routes.health.aborted', 'routes.monitor.violated', 'routes.monitor.recovered'));

-- +goose Down
-- Forward-only: retain recovery intent and committed customer notifications.
SELECT 1;
