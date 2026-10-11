-- Notify when a required MCP Tasks gauge stops arriving or becomes invalid.
-- The alert reads the app's pushed custom metrics and remains notification-only.
-- +goose Up
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct', 'mcp_tasks_oldest_age_seconds',
    'mcp_tasks_metrics_stale'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_mcp_tasks_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_tasks_notification_chk
    CHECK (metric NOT IN ('mcp_tasks_oldest_age_seconds', 'mcp_tasks_metrics_stale') OR action = 'webhook');

-- +goose Down
-- Existing rules must be removed explicitly before rollback so operators do
-- not silently lose the publisher-health alert.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'mcp_tasks_metrics_stale') THEN
        RAISE EXCEPTION 'remove mcp_tasks_metrics_stale alert rules before rollback';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_mcp_tasks_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_tasks_notification_chk
    CHECK (metric <> 'mcp_tasks_oldest_age_seconds' OR action = 'webhook');

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct', 'mcp_tasks_oldest_age_seconds'
));
