-- filename: 20261008114924411_app_health_notifications.sql

-- +goose Up
ALTER TABLE app_health_collection_state ADD COLUMN IF NOT EXISTS notification_state jsonb
 CHECK (jsonb_typeof(notification_state) = 'object' AND octet_length(notification_state::text) <= 8192);

ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
 CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'app.health.changed', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed', 'routes.health.blocked', 'routes.health.resumed', 'routes.health.aborted', 'routes.monitor.violated', 'routes.monitor.recovered', 'workflow.finished', 'routes.monitor.escalated'));

-- +goose Down
-- Forward-only: retain deduplication, cooldown state and pending deliveries.
SELECT 1;
