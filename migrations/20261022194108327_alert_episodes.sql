-- filename: 20261022194108327_alert_episodes.sql
-- migration-compat: expand
-- ADR-20261022193512: an alert rule's firing periods. meterd's evaluator
-- opens one when a rule turns firing and closes it when the rule turns ok, in
-- the same statement as the state change. One open episode per rule; a rule
-- keeps its newest 200. Deleted with the rule.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS alert_episodes (
    id uuid NOT NULL,
    rule_id uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    opened_at timestamp with time zone NOT NULL,
    resolved_at timestamp with time zone,
    CONSTRAINT alert_episodes_pkey PRIMARY KEY (id),
    CONSTRAINT alert_episodes_resolved_after_open CHECK (resolved_at IS NULL OR resolved_at >= opened_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS alert_episodes_one_open_idx ON alert_episodes (rule_id) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS alert_episodes_account_open_idx ON alert_episodes (account_id) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS alert_episodes_account_resolved_idx ON alert_episodes (account_id, resolved_at DESC) WHERE resolved_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS alert_episodes_rule_idx ON alert_episodes (rule_id, opened_at DESC);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS alert_episodes;
