-- filename: 20261003124846116_route_production_monitoring.sql

-- +goose Up
-- ADR-498: customer intent and periodic work belong to APID. No traffic mutation.
CREATE TABLE IF NOT EXISTS route_monitors (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 enabled boolean NOT NULL,
 revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
 routes jsonb NOT NULL CHECK (jsonb_typeof(routes) = 'array' AND jsonb_array_length(routes) <= 20 AND octet_length(routes::text) <= 16384),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
 next_check_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(next_check_at)),
 last_deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
 active_incident_id uuid,
 CHECK (NOT enabled OR jsonb_array_length(routes) > 0)
);
CREATE INDEX IF NOT EXISTS route_monitors_due_idx ON route_monitors(next_check_at, app_id) WHERE enabled;
CREATE TABLE IF NOT EXISTS route_monitor_incidents (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
 status text NOT NULL CHECK (status IN ('open','recovered','superseded')),
 opened_at timestamptz NOT NULL CHECK (isfinite(opened_at)),
 closed_at timestamptz CHECK (isfinite(closed_at)),
 encoded_bytes bigint NOT NULL CHECK (encoded_bytes BETWEEN 1 AND 524288),
 entry jsonb NOT NULL CHECK (jsonb_typeof(entry) = 'object' AND entry->>'version' = '1' AND entry->>'id' = id::text AND entry->>'app_id' = app_id::text AND entry->>'deployment_id' = deployment_id::text AND entry->>'revision' = revision::text AND entry->>'status' = status AND octet_length(entry::text) <= 524288),
 CHECK ((status = 'open' AND closed_at IS NULL) OR (status <> 'open' AND closed_at >= opened_at))
);
CREATE INDEX IF NOT EXISTS route_monitor_incidents_history_idx ON route_monitor_incidents(app_id, opened_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS route_monitor_incidents_one_open_idx ON route_monitor_incidents(app_id) WHERE status = 'open';
ALTER TABLE route_monitors DROP CONSTRAINT IF EXISTS route_monitors_active_incident_fk;
ALTER TABLE route_monitors ADD CONSTRAINT route_monitors_active_incident_fk FOREIGN KEY (active_incident_id) REFERENCES route_monitor_incidents(id) ON DELETE SET NULL;
-- Keep later route event types valid while this migration is replayed over retained outbox rows.
ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken', 'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached', 'routes.requirements.violated', 'routes.requirements.recovered', 'routes.requirements.changed', 'routes.health.blocked', 'routes.health.resumed', 'routes.health.aborted', 'routes.monitor.violated', 'routes.monitor.escalated', 'routes.monitor.recovered', 'workflow.finished'));
-- App-only event filters use the existing route-event scope guard.
-- +goose Down
-- Preserve saved incident evidence and pending delivery intent.
SELECT 1;
