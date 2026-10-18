-- migration-compat: expand
-- +goose Up
CREATE TABLE IF NOT EXISTS operator_alert_collection (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    last_attempt_at timestamptz,
    last_success_at timestamptz,
    CHECK (last_success_at IS NULL OR last_success_at <= last_attempt_at)
);
INSERT INTO operator_alert_collection (singleton) VALUES (true) ON CONFLICT (singleton) DO NOTHING;
CREATE TABLE IF NOT EXISTS operator_alert_rule_observations (
    rule_key text PRIMARY KEY CHECK (rule_key ~ '^[0-9a-f]{64}$'),
    evaluated_at timestamptz NOT NULL CHECK (isfinite(evaluated_at)),
    episode_ids jsonb NOT NULL CHECK (jsonb_typeof(episode_ids) = 'array')
);
CREATE TABLE IF NOT EXISTS operator_alert_episodes (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{64}$'),
    series_id text NOT NULL CHECK (series_id ~ '^[0-9a-f]{64}$'),
    rule_key text NOT NULL CHECK (rule_key ~ '^[0-9a-f]{64}$'),
    name text NOT NULL CHECK (name ~ '^[a-zA-Z_:][a-zA-Z0-9_:]*$' AND octet_length(name) <= 128),
    component text NOT NULL CHECK (component ~ '^[a-zA-Z0-9_.:-]*$' AND octet_length(component) <= 64),
    severity text NOT NULL CHECK (severity IN ('warning', 'error', 'critical')),
    account_id uuid,
    app_id uuid,
    node_id uuid,
    runbook_url text NOT NULL CHECK (octet_length(runbook_url) <= 2048),
    started_at timestamptz NOT NULL CHECK (isfinite(started_at)),
    first_seen_at timestamptz NOT NULL CHECK (isfinite(first_seen_at)),
    last_seen_at timestamptz NOT NULL CHECK (last_seen_at >= first_seen_at),
    last_evaluated_at timestamptz NOT NULL CHECK (last_evaluated_at >= started_at),
    last_checked_at timestamptz NOT NULL CHECK (last_checked_at >= last_seen_at),
    resolved_at timestamptz CHECK (resolved_at >= started_at)
);
CREATE INDEX IF NOT EXISTS operator_alert_episodes_series_idx ON operator_alert_episodes (series_id, started_at DESC, first_seen_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS operator_alert_episodes_order_idx ON operator_alert_episodes ((coalesce(resolved_at, started_at)) DESC, id DESC);
CREATE INDEX IF NOT EXISTS operator_alert_episodes_rule_idx ON operator_alert_episodes (rule_key);
CREATE UNIQUE INDEX IF NOT EXISTS operator_alert_episodes_open_idx ON operator_alert_episodes (rule_key, series_id) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS operator_alert_episodes_retention_idx ON operator_alert_episodes ((coalesce(resolved_at, last_seen_at)), id);

CREATE INDEX IF NOT EXISTS operator_alert_rule_observations_retention_idx ON operator_alert_rule_observations (evaluated_at, rule_key);

-- +goose Down
DROP TABLE operator_alert_episodes;
DROP TABLE operator_alert_rule_observations;
DROP TABLE operator_alert_collection;
