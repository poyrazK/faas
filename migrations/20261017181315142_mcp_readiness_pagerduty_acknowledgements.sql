-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_readiness_pagerduty_acknowledgements (
 rule_id uuid PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 signing_secret_sealed bytea NOT NULL CHECK(octet_length(signing_secret_sealed)<=4096),
 pagerduty_version bigint NOT NULL CHECK(pagerduty_version>=0),
 region text NOT NULL CHECK(region IN ('','us','eu')),
 fingerprint text NOT NULL CHECK(fingerprint='' OR fingerprint ~ '^[a-f0-9]{64}$'),
 service_id text NOT NULL CHECK(service_id='' OR service_id ~ '^[A-Za-z0-9_-]{1,128}$'),
 CHECK((enabled AND octet_length(signing_secret_sealed)>0 AND pagerduty_version>0 AND region<>'' AND fingerprint<>'' AND service_id<>'')
 OR (NOT enabled AND octet_length(signing_secret_sealed)=0 AND pagerduty_version=0 AND region='' AND fingerprint='' AND service_id=''))
);
CREATE TABLE IF NOT EXISTS mcp_readiness_pagerduty_ack_events (
 rule_id uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
 event_id text NOT NULL CHECK(event_id ~ '^[A-Za-z0-9_-]{1,128}$'),
 id uuid NOT NULL UNIQUE,
 incident_id uuid NOT NULL REFERENCES mcp_readiness_incidents(id) ON DELETE CASCADE,
 digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND (receipt->>'outcome' IN ('acknowledged','already_acknowledged','stale','resolved')) IS TRUE),
 received_at timestamptz NOT NULL,
 PRIMARY KEY(rule_id,event_id)
);
CREATE INDEX IF NOT EXISTS mcp_readiness_pagerduty_ack_events_incident_idx ON mcp_readiness_pagerduty_ack_events(incident_id);
CREATE INDEX IF NOT EXISTS mcp_readiness_pagerduty_ack_events_page_idx ON mcp_readiness_pagerduty_ack_events(rule_id,received_at DESC,id DESC);
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_acknowledgements)
 OR EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_ack_events)
 OR EXISTS(SELECT 1 FROM mcp_readiness_attention_actions WHERE receipt->>'source'='pagerduty') THEN
  RAISE EXCEPTION 'PagerDuty acknowledgement configuration and audit history must be retained before rollback';
 END IF;
 DROP TABLE mcp_readiness_pagerduty_ack_events;
 DROP TABLE mcp_readiness_pagerduty_acknowledgements;
END $migration$;
-- +goose StatementEnd
