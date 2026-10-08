-- +goose Up
CREATE TABLE profile_route_alert_state (
 context_key text PRIMARY KEY CHECK (length(context_key)=64),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 state jsonb NOT NULL,
 updated_at timestamptz NOT NULL
);
CREATE INDEX profile_route_alert_state_app_idx ON profile_route_alert_state(app_id);
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (event IN (
 'usage_statement.finalized','app.parked','app.woken',
 'issue.created','issue.assigned','issue.resolved','issue.reopened','issue.ignored','issue.regressed','issue.impact_threshold_reached',
 'routes.requirements.violated','routes.requirements.recovered','routes.requirements.changed',
 'routes.health.blocked','routes.health.resumed','routes.health.aborted',
 'routes.monitor.violated','routes.monitor.escalated','routes.monitor.recovered','workflow.finished',
 'profile.route_regressed','profile.route_recovered'
));
-- +goose Down
-- Preserve committed webhook events during a binary rollback.
SELECT 1;
