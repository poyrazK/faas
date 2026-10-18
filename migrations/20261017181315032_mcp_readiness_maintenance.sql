-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_readiness_maintenance_windows (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 scope text NOT NULL CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 starts_at timestamptz NOT NULL,
 ends_at timestamptz NOT NULL,
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 500),
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL,
 cancelled_at timestamptz,
 cancelled_by uuid,
 cancel_reason text,
 CHECK(ends_at>starts_at AND ends_at<=starts_at+interval '720 hours'),
 CHECK((cancelled_at IS NULL)=(cancelled_by IS NULL)),
 CHECK((cancelled_at IS NULL)=(cancel_reason IS NULL))
);
CREATE INDEX IF NOT EXISTS mcp_readiness_maintenance_app_idx ON mcp_readiness_maintenance_windows(account_id,app_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS mcp_readiness_maintenance_active_idx ON mcp_readiness_maintenance_windows(app_id,scope,ends_at) WHERE cancelled_at IS NULL;
ALTER TABLE mcp_readiness_incidents ADD COLUMN IF NOT EXISTS maintenance_pending boolean NOT NULL DEFAULT false;
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS maintenance_window_ids jsonb NOT NULL DEFAULT '[]';
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS suppression_reason text NOT NULL DEFAULT '';
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS notification_reason text NOT NULL DEFAULT '';
ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT IF EXISTS mcp_readiness_incident_events_delivery_status_check;
ALTER TABLE mcp_readiness_incident_events ADD CONSTRAINT mcp_readiness_incident_events_delivery_status_check CHECK(delivery_status IN ('pending','failed','delivered','suppressed'));
ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT IF EXISTS mcp_readiness_incident_events_incident_id_kind_key;
CREATE UNIQUE INDEX IF NOT EXISTS mcp_readiness_incident_transition_idx ON mcp_readiness_incident_events(incident_id,kind) WHERE notification_reason='';
DROP INDEX IF EXISTS mcp_readiness_incident_events_queue_idx;
CREATE INDEX mcp_readiness_incident_events_queue_idx ON mcp_readiness_incident_events(rule_id,sequence) WHERE delivery_status NOT IN ('delivered','suppressed');

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_maintenance_windows) OR EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE delivery_status='suppressed' OR notification_reason<>'') THEN
  RAISE EXCEPTION 'readiness maintenance history must be retained before rollback';
 END IF;
 DROP INDEX mcp_readiness_incident_transition_idx;
 ALTER TABLE mcp_readiness_incident_events ADD UNIQUE(incident_id,kind);
 ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT mcp_readiness_incident_events_delivery_status_check;
 ALTER TABLE mcp_readiness_incident_events ADD CHECK(delivery_status IN ('pending','failed','delivered'));
 ALTER TABLE mcp_readiness_incident_events DROP COLUMN maintenance_window_ids,DROP COLUMN suppression_reason,DROP COLUMN notification_reason;
 ALTER TABLE mcp_readiness_incidents DROP COLUMN maintenance_pending;
 DROP INDEX mcp_readiness_incident_events_queue_idx;
 CREATE INDEX mcp_readiness_incident_events_queue_idx ON mcp_readiness_incident_events(rule_id,sequence) WHERE delivery_status<>'delivered';
 DROP TABLE mcp_readiness_maintenance_windows;
END $migration$;
-- +goose StatementEnd
