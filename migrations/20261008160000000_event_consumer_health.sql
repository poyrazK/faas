-- +goose Up
ALTER TABLE event_subscription_delivery_controls ADD COLUMN paused_at timestamptz;
UPDATE event_subscription_delivery_controls SET paused_at=updated_at WHERE paused;
ALTER TABLE alert_rules ADD COLUMN event_subscription_id uuid;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text])));
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_event_consumer_chk CHECK ((metric IN ('event_pending_recipients','event_oldest_pending_seconds','event_retry_rate_per_second','event_terminal_failure_pct','event_routing_latency_p95_seconds','event_paused_seconds','event_drain_rate_per_second') AND event_subscription_id IS NOT NULL AND app_id IS NOT NULL AND action='webhook' AND window_spec IN ('5m','15m','1h','6h','24h')) OR (NOT (metric IN ('event_pending_recipients','event_oldest_pending_seconds','event_retry_rate_per_second','event_terminal_failure_pct','event_routing_latency_p95_seconds','event_paused_seconds','event_drain_rate_per_second')) AND event_subscription_id IS NULL));
CREATE INDEX event_fanout_history_consumer_window_idx ON event_fanout_attempt_history(app_id,subscription_id,occurred_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE event_subscription_id IS NOT NULL) THEN
  RAISE EXCEPTION 'delete consumer health alert rules before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX event_fanout_history_consumer_window_idx;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_event_consumer_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text])));
ALTER TABLE alert_rules DROP COLUMN event_subscription_id;
ALTER TABLE event_subscription_delivery_controls DROP COLUMN paused_at;
