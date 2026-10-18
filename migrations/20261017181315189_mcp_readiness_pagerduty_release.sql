-- +goose Up
ALTER TABLE mcp_readiness_pagerduty_acknowledgements ADD COLUMN IF NOT EXISTS sync_unacknowledgements boolean NOT NULL DEFAULT false CHECK(enabled OR NOT sync_unacknowledgements);
ALTER TABLE mcp_readiness_pagerduty_ack_events ADD COLUMN IF NOT EXISTS delivery_destination text NOT NULL DEFAULT '' CHECK(delivery_destination IN ('','primary','fallback'));
ALTER TABLE mcp_readiness_pagerduty_ack_events DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_ack_events_pkey;
ALTER TABLE mcp_readiness_pagerduty_ack_events ADD PRIMARY KEY(rule_id,event_id,delivery_destination);
ALTER TABLE mcp_readiness_pagerduty_ack_events DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_ack_events_receipt_check;
ALTER TABLE mcp_readiness_pagerduty_ack_events ADD CONSTRAINT mcp_readiness_pagerduty_ack_events_receipt_check CHECK(jsonb_typeof(receipt)='object' AND (receipt->>'outcome' IN ('acknowledged','already_acknowledged','stale','resolved','released','retained','not_acknowledged')) IS TRUE);
ALTER TABLE mcp_readiness_pagerduty_ack_events DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_ack_delivery_check;
ALTER TABLE mcp_readiness_pagerduty_ack_events ADD CONSTRAINT mcp_readiness_pagerduty_ack_delivery_check CHECK(
 (delivery_destination='' AND coalesce(receipt->>'action','acknowledge')='acknowledge') OR
 (delivery_destination IN ('primary','fallback') AND (receipt->>'action'='release' AND receipt->>'destination'=delivery_destination) IS TRUE)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION mcp_readiness_pagerduty_states_valid(s jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT
 (NOT(s?'local_decision_at') OR jsonb_typeof(s->'local_decision_at')='string') AND
 (NOT(s?'pagerduty_tracking') OR jsonb_typeof(s->'pagerduty_tracking')='boolean') AND
 (NOT(s?'pagerduty_states') OR CASE WHEN jsonb_typeof(s->'pagerduty_states')='object' THEN
  (s->>'pagerduty_tracking'='true') IS TRUE AND
  (NOT EXISTS(SELECT 1 FROM jsonb_each(s->'pagerduty_states') d WHERE d.value->>'active'='true') OR (s->'acknowledgement'->>'source'='pagerduty') IS TRUE) AND NOT EXISTS(
   SELECT 1 FROM jsonb_each(s->'pagerduty_states') d WHERE NOT coalesce(
    d.key IN ('primary','fallback') AND jsonb_typeof(d.value)='object' AND
    jsonb_typeof(d.value->'active')='boolean' AND
    (NOT(d.value?'ambiguous') OR jsonb_typeof(d.value->'ambiguous')='boolean') AND
    jsonb_typeof(d.value->'last_occurred_at')='string' AND d.value->>'fingerprint' ~ '^[a-f0-9]{64}$' AND
    jsonb_typeof(d.value->'hold')='object' AND d.value->'hold'->>'destination'=d.key AND
    d.value->'hold'->>'provider_incident_id' ~ '^[A-Za-z0-9_-]{1,128}$' AND
    d.value->'hold'->>'provider_event_id' ~ '^[A-Za-z0-9_-]{1,128}$' AND
    d.value->'hold'->>'service_id' ~ '^[A-Za-z0-9_-]{1,128}$' AND
    d.value->'hold'->>'region' IN ('us','eu') AND
    jsonb_typeof(d.value->'hold'->'occurred_at')='string' AND
    jsonb_typeof(d.value->'hold'->'received_at')='string' AND
    jsonb_typeof(d.value->'hold'->'policy_version')='number' AND
    d.value->'hold'->>'policy_version' ~ '^[1-9][0-9]{0,18}$' AND
    (d.value->'hold'->>'policy_version')::numeric<=9223372036854775807 AND
    jsonb_typeof(d.value->'hold'->'release_sync_enabled')='boolean',false)
  ) ELSE false END);
$$;
-- +goose StatementEnd
ALTER TABLE mcp_readiness_incident_attention DROP CONSTRAINT IF EXISTS mcp_readiness_pagerduty_states_check;
ALTER TABLE mcp_readiness_incident_attention ADD CONSTRAINT mcp_readiness_pagerduty_states_check CHECK(mcp_readiness_pagerduty_states_valid(snapshot));
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_acknowledgements WHERE sync_unacknowledgements)
 OR EXISTS(SELECT 1 FROM mcp_readiness_pagerduty_ack_events WHERE delivery_destination<>'' OR receipt->>'action'='release')
 OR EXISTS(SELECT 1 FROM mcp_readiness_incident_attention WHERE snapshot?'pagerduty_states' OR snapshot->>'pagerduty_tracking'='true') THEN
  RAISE EXCEPTION 'PagerDuty release configuration, ownership, ordering and audit history must be retained before rollback';
 END IF;
 ALTER TABLE mcp_readiness_incident_attention DROP CONSTRAINT mcp_readiness_pagerduty_states_check;
 DROP FUNCTION mcp_readiness_pagerduty_states_valid(jsonb);
 ALTER TABLE mcp_readiness_pagerduty_ack_events DROP CONSTRAINT mcp_readiness_pagerduty_ack_delivery_check;
 ALTER TABLE mcp_readiness_pagerduty_ack_events DROP CONSTRAINT mcp_readiness_pagerduty_ack_events_receipt_check;
 ALTER TABLE mcp_readiness_pagerduty_ack_events ADD CONSTRAINT mcp_readiness_pagerduty_ack_events_receipt_check CHECK(jsonb_typeof(receipt)='object' AND (receipt->>'outcome' IN ('acknowledged','already_acknowledged','stale','resolved')) IS TRUE);
 ALTER TABLE mcp_readiness_pagerduty_ack_events DROP CONSTRAINT mcp_readiness_pagerduty_ack_events_pkey;
 ALTER TABLE mcp_readiness_pagerduty_ack_events DROP COLUMN delivery_destination;
 ALTER TABLE mcp_readiness_pagerduty_ack_events ADD PRIMARY KEY(rule_id,event_id);
 ALTER TABLE mcp_readiness_pagerduty_acknowledgements DROP COLUMN sync_unacknowledgements;
END $migration$;
-- +goose StatementEnd
