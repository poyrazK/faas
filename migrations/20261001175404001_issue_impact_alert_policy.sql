-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_issue_impact_alert_policies (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    minimum_customers integer NOT NULL CHECK (minimum_customers BETWEEN 1 AND 10000),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE issue_activity DROP CONSTRAINT IF EXISTS issue_activity_action_check;
ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check
    CHECK (action IN ('created','assigned','resolved','reopened','ignored','regressed','impact_threshold_reached'));

ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk
    CHECK (event IN ('usage_statement.finalized','app.parked','app.woken','issue.created','issue.assigned','issue.resolved','issue.reopened','issue.ignored','issue.regressed','issue.impact_threshold_reached'));
-- +goose StatementEnd

-- +goose Down
-- Keep policy and audit data during rollback, following ADR-380's durable
-- issue-history rule. New event rows remain valid if the feature is rolled
-- back and later restored.
SELECT 1;
