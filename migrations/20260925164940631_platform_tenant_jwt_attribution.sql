-- filename: 20260925164940631_platform_tenant_jwt_attribution.sql

-- +goose Up
-- +goose StatementBegin
-- Retain only the authorization rule identity in usage data; JWT claim values
-- are resolved at request time and are never persisted as financial metadata.
ALTER TABLE api_consumer_usage_events
  ADD COLUMN IF NOT EXISTS platform_tenant_jwt_authorization_rule_id uuid;

ALTER TABLE api_consumer_usage_events
  DROP CONSTRAINT IF EXISTS api_consumer_usage_events_tenant_source_chk;
ALTER TABLE api_consumer_usage_events
  ADD CONSTRAINT api_consumer_usage_events_tenant_source_chk
  CHECK (
    (consumer_key <> '__anonymous__' AND platform_tenant_surface_id IS NULL AND
      platform_tenant_jwt_authorization_rule_id IS NULL)
    OR
    (consumer_key = '__anonymous__' AND
      ((platform_tenant_id IS NULL AND platform_tenant_surface_id IS NULL AND platform_tenant_jwt_authorization_rule_id IS NULL)
       OR (platform_tenant_id IS NOT NULL AND num_nonnulls(platform_tenant_surface_id, platform_tenant_jwt_authorization_rule_id) = 1)))
  ) NOT VALID;

ALTER TABLE platform_tenant_usage_minutes
  DROP CONSTRAINT IF EXISTS platform_tenant_usage_minutes_source_kind_chk;
ALTER TABLE platform_tenant_usage_minutes
  ADD CONSTRAINT platform_tenant_usage_minutes_source_kind_chk
  CHECK (source_kind IN ('consumer', 'surface', 'jwt'));

-- Preserve JWT-rule participation for immutable tenant-statement handoffs.
CREATE TABLE IF NOT EXISTS platform_tenant_statement_jwt_rules (
  statement_id uuid NOT NULL REFERENCES platform_tenant_statements(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  authorization_rule_id uuid NOT NULL,
  PRIMARY KEY (statement_id, app_id, authorization_rule_id)
);
CREATE INDEX IF NOT EXISTS platform_tenant_statement_jwt_rules_lookup_idx
  ON platform_tenant_statement_jwt_rules (app_id, authorization_rule_id, statement_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_statement_jwt_rules;
DELETE FROM platform_tenant_usage_minutes WHERE source_kind = 'jwt';
ALTER TABLE platform_tenant_usage_minutes
  DROP CONSTRAINT IF EXISTS platform_tenant_usage_minutes_source_kind_chk;
ALTER TABLE platform_tenant_usage_minutes
  ADD CONSTRAINT platform_tenant_usage_minutes_source_kind_chk
  CHECK (source_kind IN ('consumer', 'surface'));
ALTER TABLE api_consumer_usage_events
  DROP CONSTRAINT IF EXISTS api_consumer_usage_events_tenant_source_chk;
ALTER TABLE api_consumer_usage_events
  ADD CONSTRAINT api_consumer_usage_events_tenant_source_chk
  CHECK ((platform_tenant_surface_id IS NULL OR
          (platform_tenant_id IS NOT NULL AND consumer_key = '__anonymous__')) AND
         (platform_tenant_id IS NULL OR consumer_key <> '__anonymous__' OR
          platform_tenant_surface_id IS NOT NULL)) NOT VALID;
ALTER TABLE api_consumer_usage_events
  DROP COLUMN IF EXISTS platform_tenant_jwt_authorization_rule_id;
-- +goose StatementEnd
