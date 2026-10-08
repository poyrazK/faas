-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_recovery_jobs
    ADD COLUMN IF NOT EXISTS last_progress_at timestamptz,
    ADD COLUMN IF NOT EXISTS wait_reason text NOT NULL DEFAULT '' CHECK (wait_reason IN ('','capacity','legacy_claim'));
DO $$
BEGIN
    -- Preserve a later migration's broader contract when replaying this one.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk' AND strpos(pg_get_constraintdef(oid), 'event_recovery_expiring_jobs') > 0) THEN
        ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text,'event_execution_dead_letters'::text,'event_execution_dead_letter_rate_per_second'::text,'event_handler_failure_pct'::text,'event_completion_latency_p95_seconds'::text,'event_recovery_stalled_jobs'::text,'event_recovery_expiring_jobs'::text])));
    END IF;
END $$;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_recovery_health_chk') THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_recovery_health_chk CHECK (metric NOT IN ('event_recovery_stalled_jobs','event_recovery_expiring_jobs') OR (app_id IS NOT NULL AND event_subscription_id IS NULL AND action='webhook' AND window_spec IN ('5m','15m','1h','6h','24h')));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('event_recovery_stalled_jobs','event_recovery_expiring_jobs')) THEN RAISE EXCEPTION 'delete recovery health alert rules before rollback'; END IF;
END $$;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_recovery_health_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text,'event_execution_dead_letters'::text,'event_execution_dead_letter_rate_per_second'::text,'event_handler_failure_pct'::text,'event_completion_latency_p95_seconds'::text])));
ALTER TABLE event_recovery_jobs DROP COLUMN wait_reason, DROP COLUMN last_progress_at;
-- +goose StatementEnd
