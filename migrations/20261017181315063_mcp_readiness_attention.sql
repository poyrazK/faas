-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_readiness_incident_attention (
 incident_id uuid PRIMARY KEY REFERENCES mcp_readiness_incidents(id) ON DELETE CASCADE,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND coalesce((snapshot->>'version')::bigint,0)>=0 AND coalesce((snapshot->>'count')::integer,0) BETWEEN 0 AND 10)
);
CREATE TABLE IF NOT EXISTS mcp_readiness_attention_actions (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 incident_id uuid NOT NULL REFERENCES mcp_readiness_incidents(id) ON DELETE CASCADE,
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,request_id)
);
CREATE INDEX IF NOT EXISTS mcp_readiness_attention_actions_incident_idx ON mcp_readiness_attention_actions(incident_id,created_at DESC,request_id DESC);
ALTER TABLE mcp_readiness_incident_events ADD COLUMN IF NOT EXISTS reminder_number integer NOT NULL DEFAULT 0 CHECK(reminder_number BETWEEN 0 AND 10);
ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT IF EXISTS mcp_readiness_reminder_kind_check;
ALTER TABLE mcp_readiness_incident_events ADD CONSTRAINT mcp_readiness_reminder_kind_check CHECK((notification_reason='reminder')=(reminder_number>0));
CREATE UNIQUE INDEX IF NOT EXISTS mcp_readiness_incident_reminder_idx ON mcp_readiness_incident_events(incident_id,reminder_number) WHERE reminder_number>0;

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_attention_actions)
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE reminder_number>0)
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_attention WHERE coalesce((snapshot->>'version')::bigint,0)>0) THEN
  RAISE EXCEPTION 'readiness acknowledgement and reminder history must be retained before rollback';
 END IF;
 DROP TABLE mcp_readiness_attention_actions;
 DROP TABLE mcp_readiness_incident_attention;
 DROP INDEX mcp_readiness_incident_reminder_idx;
 ALTER TABLE mcp_readiness_incident_events DROP CONSTRAINT mcp_readiness_reminder_kind_check;
 ALTER TABLE mcp_readiness_incident_events DROP COLUMN reminder_number;
END $migration$;
-- +goose StatementEnd
