-- +goose Up
ALTER TABLE mcp_readiness_fallback_policies
 ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT 'webhook' CHECK(provider IN ('webhook','pagerduty')),
 ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '' CHECK(region IN ('','us','eu')),
 ADD COLUMN IF NOT EXISTS integration_key_sealed bytea NOT NULL DEFAULT '\x' CHECK(octet_length(integration_key_sealed)<=4096),
 ADD COLUMN IF NOT EXISTS fingerprint text NOT NULL DEFAULT '' CHECK(fingerprint='' OR fingerprint ~ '^[a-f0-9]{64}$');
ALTER TABLE mcp_readiness_fallback_policies DROP CONSTRAINT IF EXISTS mcp_readiness_fallback_policies_check;
ALTER TABLE mcp_readiness_fallback_policies ADD CONSTRAINT mcp_readiness_fallback_policies_check CHECK(
 (NOT enabled AND provider='webhook' AND webhook_url='' AND octet_length(webhook_secret_sealed)=0 AND after_failures=0 AND region='' AND octet_length(integration_key_sealed)=0 AND fingerprint='') OR
 (enabled AND after_failures>0 AND (
  (provider='webhook' AND webhook_url<>'' AND octet_length(webhook_secret_sealed)>0 AND region='' AND octet_length(integration_key_sealed)=0 AND fingerprint='') OR
  (provider='pagerduty' AND webhook_url='' AND octet_length(webhook_secret_sealed)=0 AND region IN ('us','eu') AND octet_length(integration_key_sealed)>0 AND fingerprint<>'')
 ))
);
ALTER TABLE mcp_readiness_incidents DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_fallback_route_check;
ALTER TABLE mcp_readiness_incidents ADD CONSTRAINT mcp_readiness_pagerduty_fallback_route_check CHECK(
 NOT (delivery_routing->'fallback' ? 'provider') OR
 ((delivery_routing->'fallback'->>'provider' IN ('webhook','pagerduty')) IS TRUE AND
 (delivery_routing->'fallback'->>'provider'<>'pagerduty' OR
 (delivery_routing->'fallback'->>'url' IN ('https://events.pagerduty.com/v2/enqueue','https://events.eu.pagerduty.com/v2/enqueue') AND delivery_routing->'fallback'->>'receiver_id' ~ '^[a-f0-9]{64}$' AND length(delivery_routing->'fallback'->>'secret')>0) IS TRUE))
);
ALTER TABLE mcp_readiness_pagerduty_acknowledgements
 ADD COLUMN IF NOT EXISTS destination text NOT NULL DEFAULT 'primary' CHECK(destination IN ('primary','fallback')),
 ADD COLUMN IF NOT EXISTS fallback_version bigint NOT NULL DEFAULT 0 CHECK(fallback_version>=0);
ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_acknowledgements_check;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD CONSTRAINT mcp_readiness_pagerduty_acknowledgements_check CHECK(
 (enabled AND octet_length(signing_secret_sealed)>0 AND region<>'' AND fingerprint<>'' AND service_id<>'' AND
 ((destination='primary' AND pagerduty_version>0 AND fallback_version=0) OR (destination='fallback' AND pagerduty_version=0 AND fallback_version>0))) OR
 (NOT enabled AND destination='primary' AND octet_length(signing_secret_sealed)=0 AND pagerduty_version=0 AND fallback_version=0 AND region='' AND fingerprint='' AND service_id='')
);
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_fallback_policies WHERE provider='pagerduty')
 OR EXISTS(SELECT 1 FROM mcp_readiness_incidents WHERE delivery_routing->'fallback'->>'provider'='pagerduty')
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_events WHERE destination_deliveries->'fallback'->>'provider'='pagerduty')
 OR EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_acknowledgements WHERE destination='fallback')
 OR EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_ack_events WHERE receipt->>'destination'='fallback') THEN
  RAISE EXCEPTION 'PagerDuty fallback configuration, delivery and acknowledgement history must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_acknowledgements_check;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP COLUMN destination, DROP COLUMN fallback_version;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD CONSTRAINT mcp_readiness_pagerduty_acknowledgements_check CHECK(
 (enabled AND octet_length(signing_secret_sealed)>0 AND pagerduty_version>0 AND region<>'' AND fingerprint<>'' AND service_id<>'') OR
 (NOT enabled AND octet_length(signing_secret_sealed)=0 AND pagerduty_version=0 AND region='' AND fingerprint='' AND service_id=''));
 ALTER TABLE mcp_readiness_incidents DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_fallback_route_check;
 ALTER TABLE mcp_readiness_fallback_policies DROP CONSTRAINT IF EXISTS mcp_readiness_fallback_policies_check;
 ALTER TABLE mcp_readiness_fallback_policies DROP COLUMN provider, DROP COLUMN region, DROP COLUMN integration_key_sealed, DROP COLUMN fingerprint;
 ALTER TABLE mcp_readiness_fallback_policies ADD CONSTRAINT mcp_readiness_fallback_policies_check CHECK(
 (enabled AND webhook_url<>'' AND octet_length(webhook_secret_sealed)>0 AND after_failures>0) OR
 (NOT enabled AND webhook_url='' AND octet_length(webhook_secret_sealed)=0 AND after_failures=0));
END $migration$;
-- +goose StatementEnd
