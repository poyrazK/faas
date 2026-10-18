-- +goose Up
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD COLUMN IF NOT EXISTS legacy_receiver boolean NOT NULL DEFAULT true;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ALTER COLUMN legacy_receiver SET DEFAULT false;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_acknowledgements_pkey;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD PRIMARY KEY(rule_id,destination);
CREATE UNIQUE INDEX IF NOT EXISTS mcp_readiness_pagerduty_ack_legacy_idx ON mcp_readiness_pagerduty_acknowledgements(rule_id) WHERE legacy_receiver;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_acknowledgements_check;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD CONSTRAINT mcp_readiness_pagerduty_acknowledgements_check CHECK(
 (enabled AND octet_length(signing_secret_sealed)>0 AND region<>'' AND fingerprint<>'' AND service_id<>'' AND
 ((destination='primary' AND pagerduty_version>0 AND fallback_version=0) OR (destination='fallback' AND pagerduty_version=0 AND fallback_version>0))) OR
 (NOT enabled AND octet_length(signing_secret_sealed)=0 AND pagerduty_version=0 AND fallback_version=0 AND region='' AND fingerprint='' AND service_id='')
);
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_acknowledgements WHERE NOT legacy_receiver OR (NOT enabled AND destination='fallback')) THEN
  RAISE EXCEPTION 'Independent PagerDuty acknowledgement bindings must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP CONSTRAINT mcp_readiness_pagerduty_acknowledgements_check;
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD CONSTRAINT mcp_readiness_pagerduty_acknowledgements_check CHECK(
 (enabled AND octet_length(signing_secret_sealed)>0 AND region<>'' AND fingerprint<>'' AND service_id<>'' AND
 ((destination='primary' AND pagerduty_version>0 AND fallback_version=0) OR (destination='fallback' AND pagerduty_version=0 AND fallback_version>0))) OR
 (NOT enabled AND destination='primary' AND octet_length(signing_secret_sealed)=0 AND pagerduty_version=0 AND fallback_version=0 AND region='' AND fingerprint='' AND service_id='')
);
 DROP INDEX mcp_readiness_pagerduty_ack_legacy_idx;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP CONSTRAINT mcp_readiness_pagerduty_acknowledgements_pkey;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP COLUMN legacy_receiver;
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD PRIMARY KEY(rule_id);
END $migration$;
-- +goose StatementEnd
