-- filename: 20261015173127929_operator_alert_triage_history.sql

-- +goose NO TRANSACTION

-- +goose Up
-- Invalid historical revision values index as NULL instead of breaking DDL.
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_operator_alert_triage_episode_revision_idx
ON audit_log ((data->>'episode_id'), (CASE WHEN jsonb_typeof(data#>'{triage,revision}') = 'number' AND (data#>>'{triage,revision}') ~ '^[1-9][0-9]{0,15}$' THEN (data#>>'{triage,revision}')::bigint END) DESC, id DESC)
WHERE kind = 'operator.action.alert_triage';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS audit_log_operator_alert_triage_episode_revision_idx;
