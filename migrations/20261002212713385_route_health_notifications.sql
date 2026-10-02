-- +goose Up
CREATE TABLE IF NOT EXISTS route_health_notification_state (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    context_key text NOT NULL CHECK (context_key ~ '^[0-9a-f]{64}$'),
    status text NOT NULL CHECK (status IN ('clear', 'blocked_unknown', 'blocked_regressed')),
    blocked_decision_id uuid,
    updated_at timestamptz NOT NULL CHECK (isfinite(updated_at)),
    CHECK ((status = 'clear' AND blocked_decision_id IS NULL) OR (status <> 'clear' AND blocked_decision_id IS NOT NULL))
);

-- Keep the final route event vocabulary during replay so retained events
-- written by later migrations remain valid.
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
    CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed', 'routes.health.blocked', 'routes.health.resumed', 'routes.health.aborted'));

-- +goose Down
-- Forward-only: retain transition state and pending customer notifications.
SELECT 1;
