-- +goose Up
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS delivery_version bigint NOT NULL DEFAULT 0 CHECK(delivery_version>=0);
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS last_operator_action jsonb;
ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT IF EXISTS mcp_readiness_incident_events_delivery_status_check;
ALTER TABLE mcp_readiness_incident_events ADD CONSTRAINT mcp_readiness_incident_events_delivery_status_check CHECK(delivery_status IN ('pending','failed','delivered','suppressed','abandoned'));
DROP INDEX IF EXISTS mcp_readiness_incident_events_queue_idx;
CREATE INDEX mcp_readiness_incident_events_queue_idx ON mcp_readiness_incident_events(rule_id,sequence) WHERE delivery_status NOT IN ('delivered','suppressed','abandoned');
CREATE TABLE IF NOT EXISTS mcp_readiness_notification_actions (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 event_id uuid NOT NULL REFERENCES mcp_readiness_incident_events(id) ON DELETE CASCADE,
 action text NOT NULL CHECK(action IN ('retry','abandon')),
 expected_version bigint NOT NULL CHECK(expected_version>=0),
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 500),
 receipt jsonb NOT NULL,
 PRIMARY KEY(app_id,request_id)
);
CREATE INDEX IF NOT EXISTS mcp_readiness_notification_actions_event_idx ON mcp_readiness_notification_actions(event_id);

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_notification_actions) OR EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE delivery_status='abandoned' OR last_operator_action IS NOT NULL) THEN
  RAISE EXCEPTION 'readiness notification recovery history must be retained before rollback';
 END IF;
 DROP TABLE mcp_readiness_notification_actions;
 ALTER TABLE mcp_readiness_incident_events DROP COLUMN delivery_version,DROP COLUMN last_operator_action;
 ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT mcp_readiness_incident_events_delivery_status_check;
 ALTER TABLE mcp_readiness_incident_events ADD CONSTRAINT mcp_readiness_incident_events_delivery_status_check CHECK(delivery_status IN ('pending','failed','delivered','suppressed'));
 DROP INDEX mcp_readiness_incident_events_queue_idx;
 CREATE INDEX mcp_readiness_incident_events_queue_idx ON mcp_readiness_incident_events(rule_id,sequence) WHERE delivery_status NOT IN ('delivered','suppressed');
END $migration$;
-- +goose StatementEnd
