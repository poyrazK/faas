-- filename: 20261006105405005_route_monitor_escalation_webhook.sql

-- +goose Up
-- +goose StatementBegin
-- Escalation transitions use the existing app-webhook outbox and delivery path.
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed', 'routes.health.blocked', 'routes.health.resumed', 'routes.health.aborted', 'routes.monitor.violated', 'routes.monitor.escalated', 'routes.monitor.recovered', 'workflow.finished'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Preserve queued escalation intent and the delivery ledger during rollback.
SELECT 1;
-- +goose StatementEnd
