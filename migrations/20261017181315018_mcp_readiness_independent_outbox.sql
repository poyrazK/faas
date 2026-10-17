-- +goose Up
-- +goose StatementBegin
DO $migration$ BEGIN
 IF to_regclass('public.mcp_readiness_incident_events') IS NOT NULL THEN RETURN; END IF;
 CREATE TABLE mcp_readiness_incidents (
  id uuid PRIMARY KEY,
  rule_id uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  scope text NOT NULL DEFAULT '',
  coverage text NOT NULL,
  opened_at timestamptz NOT NULL,
  resolved_at timestamptz,
  UNIQUE(id,rule_id),
  CHECK(resolved_at IS NULL OR resolved_at>=opened_at)
 );
 CREATE UNIQUE INDEX mcp_readiness_incidents_open_rule_idx ON mcp_readiness_incidents(rule_id) WHERE resolved_at IS NULL;
 CREATE INDEX mcp_readiness_incidents_app_idx ON mcp_readiness_incidents(account_id,app_id);
 CREATE TABLE mcp_readiness_incident_cursors (
  rule_id uuid PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
  last_observed_at timestamptz NOT NULL
 );
 CREATE TABLE mcp_readiness_incident_events (
  id uuid PRIMARY KEY,
  incident_id uuid NOT NULL,
  rule_id uuid NOT NULL,
  kind text NOT NULL CHECK(kind IN ('alert.fired','alert.resolved')),
  sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  occurred_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  observed_value double precision NOT NULL,
  delivery_status text NOT NULL DEFAULT 'pending' CHECK(delivery_status IN ('pending','failed','delivered')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
  available_at timestamptz NOT NULL,
  lease_token uuid,
  lease_until timestamptz,
  last_delivery_id uuid REFERENCES alert_deliveries(id) ON DELETE SET NULL,
  last_attempt_at timestamptz,
  last_status_code integer,
  imported boolean NOT NULL DEFAULT false,
  FOREIGN KEY(incident_id,rule_id) REFERENCES mcp_readiness_incidents(id,rule_id) ON DELETE CASCADE,
  UNIQUE(incident_id,kind),
  CHECK((lease_token IS NULL)=(lease_until IS NULL))
 );
 CREATE INDEX mcp_readiness_incident_events_queue_idx ON mcp_readiness_incident_events(rule_id,sequence) WHERE delivery_status<>'delivered';
 CREATE INDEX mcp_readiness_incident_events_timeline_idx ON mcp_readiness_incident_events(occurred_at DESC,id DESC);
 CREATE INDEX mcp_readiness_incident_events_rule_timeline_idx ON mcp_readiness_incident_events(rule_id,occurred_at DESC,id DESC);

 -- Import one logical event from the most recent transport attempt, preserving
 -- stable IDs, payloads, and delivered/failed state from the previous journal.
 DROP TABLE IF EXISTS pg_temp.mcp_readiness_legacy_events;
 CREATE TEMP TABLE mcp_readiness_legacy_events ON COMMIT DROP AS
 SELECT DISTINCT ON (d.payload->>'event_id') d.*,count(*) OVER(PARTITION BY d.payload->>'event_id') AS logical_attempts
 FROM alert_deliveries d
 WHERE d.is_test=false AND d.payload->>'metric'='mcp_readiness_unhealthy'
 AND d.payload->>'event' IN ('alert.fired','alert.resolved')
 AND d.payload->>'event_id' ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' AND d.payload->>'incident_id' ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
 ORDER BY d.payload->>'event_id',d.fired_at DESC,d.id DESC;
 INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at,resolved_at)
 SELECT (payload->>'incident_id')::uuid,rule_id,account_id,app_id,coalesce(payload->>'mcp_readiness_scope',''),coalesce(payload->>'mcp_readiness_coverage_fingerprint',''),
 min(coalesce((payload->>'incident_started_at')::timestamptz,(payload->>'occurred_at')::timestamptz,fired_at)),
 min(coalesce((payload->>'occurred_at')::timestamptz,fired_at)) FILTER(WHERE payload->>'event'='alert.resolved')
 FROM mcp_readiness_legacy_events GROUP BY payload->>'incident_id',rule_id,account_id,app_id,coalesce(payload->>'mcp_readiness_scope',''),coalesce(payload->>'mcp_readiness_coverage_fingerprint','');
 INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,delivery_status,attempt_count,available_at,last_delivery_id,last_attempt_at,last_status_code,imported)
 SELECT (d.payload->>'event_id')::uuid,(d.payload->>'incident_id')::uuid,d.rule_id,d.payload->>'event',coalesce((d.payload->>'occurred_at')::timestamptz,d.fired_at),d.payload,d.observed_value,d.status,d.logical_attempts::integer,d.fired_at+make_interval(mins=>r.cooldown_minutes),d.id,d.fired_at,d.last_status_code,true
 FROM mcp_readiness_legacy_events d JOIN alert_rules r ON r.id=d.rule_id
 ORDER BY coalesce((d.payload->>'occurred_at')::timestamptz,d.fired_at),d.payload->>'event',d.id;
 INSERT INTO mcp_readiness_incident_cursors(rule_id,last_observed_at)
 SELECT rule_id,max(occurred_at) FROM mcp_readiness_incident_events GROUP BY rule_id;
END $migration$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE imported=false OR lease_token IS NOT NULL) THEN RAISE EXCEPTION 'independent readiness events or active deliveries must be retained before rollback'; END IF;
 DROP TABLE mcp_readiness_incident_events;
 DROP TABLE mcp_readiness_incident_cursors;
 DROP TABLE mcp_readiness_incidents;
END $migration$;
-- +goose StatementEnd
