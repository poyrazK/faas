-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_readiness_reminder_defaults (
 rule_id uuid PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK(version>0),
 interval_seconds integer NOT NULL CHECK(interval_seconds=0 OR interval_seconds BETWEEN 60 AND 86400),
 max_reminders integer NOT NULL CHECK(max_reminders BETWEEN 0 AND 10),
 CHECK((interval_seconds=0 AND max_reminders=0) OR (interval_seconds>0 AND max_reminders>0))
);
ALTER TABLE mcp_readiness_incident_attention DROP CONSTRAINT IF EXISTS mcp_readiness_reminder_source_check;
ALTER TABLE mcp_readiness_incident_attention ADD CONSTRAINT mcp_readiness_reminder_source_check CHECK(
 (NOT snapshot ? 'policy_source' OR (snapshot->>'policy_source' IN ('rule_default','incident_override','legacy')) IS TRUE)
 AND (NOT snapshot ? 'defaults_version' OR ((snapshot->>'defaults_version')::bigint>=0) IS TRUE)
 AND (coalesce(snapshot->>'policy_source','legacy')<>'rule_default' OR snapshot ? 'defaults_version')
);
-- Existing snapshots are intentionally untouched. Only newly opened incidents inherit.

-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_reminder_defaults)
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_attention WHERE snapshot ? 'defaults_version') THEN
  RAISE EXCEPTION 'readiness reminder defaults and inherited policy history must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_incident_attention DROP CONSTRAINT mcp_readiness_reminder_source_check;
 DROP TABLE mcp_readiness_reminder_defaults;
END $migration$;
-- +goose StatementEnd
