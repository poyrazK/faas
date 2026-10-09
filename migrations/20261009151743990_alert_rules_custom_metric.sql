-- +goose Up
-- ADR-745 slice 4: a custom_metric alert names one pushed metric
-- (app_custom_metrics.name) and is app-scoped, webhook-only, and limited
-- to windows the metric's Prometheus history can answer meaningfully.
-- +goose StatementBegin
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS custom_metric_name text;
DO $$
BEGIN
    -- Preserve a later migration's broader contract when replaying this one.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk' AND strpos(pg_get_constraintdef(oid), 'custom_metric') > 0) THEN
        ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text, 'custom_metric'::text])));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_custom_metric_chk') THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_custom_metric_chk CHECK (((metric = 'custom_metric') = (custom_metric_name IS NOT NULL)) AND (metric <> 'custom_metric' OR (app_id IS NOT NULL AND event_subscription_id IS NULL AND action = 'webhook' AND window_spec IN ('5m','15m','1h','6h','24h') AND custom_metric_name ~ '^[a-z][a-z0-9_]{0,62}$')));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE metric='custom_metric') THEN RAISE EXCEPTION 'delete custom_metric alert rules before rollback'; END IF; END $$;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_custom_metric_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text])));
ALTER TABLE alert_rules DROP COLUMN custom_metric_name;
-- +goose StatementEnd
