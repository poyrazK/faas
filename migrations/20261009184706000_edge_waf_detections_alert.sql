-- +goose Up
-- ADR-831 step 1: an opt-in, notification-only preset on kind=waf detections.
-- The WAF is observe-only, so detections never reach
-- gateway_edge_rejections_total and edge_rejection_pressure cannot see them.
-- Like the other edge security metrics, external traffic drives this one, so
-- it may only send webhooks. Pro and above, matching kind=waf availability.
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct',
    'event_pending_recipients', 'event_oldest_pending_seconds', 'event_retry_rate_per_second',
    'event_terminal_failure_pct', 'event_routing_latency_p95_seconds', 'event_paused_seconds',
    'event_drain_rate_per_second', 'event_execution_dead_letters',
    'event_execution_dead_letter_rate_per_second', 'event_handler_failure_pct',
    'event_completion_latency_p95_seconds', 'event_recovery_stalled_jobs',
    'event_recovery_expiring_jobs', 'event_recovery_capacity_wait_jobs',
    'workflow_failures', 'workflow_schedule_quota_skips', 'workflow_pending_age_seconds',
    'workflow_waiting_age_seconds', 'workflow_due_age_seconds',
    'pre_auth_pressure', 'edge_validation_failures', 'edge_rejections', 'edge_waf_detections'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_edge_security_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_edge_security_notification_chk
    CHECK (metric NOT IN ('pre_auth_pressure', 'edge_validation_failures', 'edge_rejections', 'edge_waf_detections') OR action = 'webhook');

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds', 'cert_issuance_failed',
    'queue_depth', 'new_error_fingerprint', 'daily_cost_cents', 'slo_burn_rate',
    'canary_stuck_step', 'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing', 'canary_fleet_in_flight_high',
    'pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct', 'workflow_due_age_seconds',
    'pre_auth_pressure', 'edge_validation_failures', 'edge_rejections', 'edge_waf_detections'
));

INSERT INTO alert_presets (
    name, display_name, description, category, metric, comparison, threshold,
    window_spec, default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES (
    'edge_waf_detections',
    'Edge WAF detections',
    'Alerts when kind=waf edge rules detect more than 25 likely attacks in 15 minutes. The WAF observes only; nothing is blocked. Notification only.',
    'security', 'edge_waf_detections', 'gt', 25, '15m', 60, true, 'pro'
) ON CONFLICT (name) DO NOTHING;

-- +goose Down
DELETE FROM alert_presets WHERE name = 'edge_waf_detections';
DELETE FROM alert_rules WHERE metric = 'edge_waf_detections';

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds', 'cert_issuance_failed',
    'queue_depth', 'new_error_fingerprint', 'daily_cost_cents', 'slo_burn_rate',
    'canary_stuck_step', 'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing', 'canary_fleet_in_flight_high',
    'pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct', 'workflow_due_age_seconds',
    'pre_auth_pressure', 'edge_validation_failures', 'edge_rejections'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_edge_security_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_edge_security_notification_chk
    CHECK (metric NOT IN ('pre_auth_pressure', 'edge_validation_failures', 'edge_rejections') OR action = 'webhook');

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct',
    'event_pending_recipients', 'event_oldest_pending_seconds', 'event_retry_rate_per_second',
    'event_terminal_failure_pct', 'event_routing_latency_p95_seconds', 'event_paused_seconds',
    'event_drain_rate_per_second', 'event_execution_dead_letters',
    'event_execution_dead_letter_rate_per_second', 'event_handler_failure_pct',
    'event_completion_latency_p95_seconds', 'event_recovery_stalled_jobs',
    'event_recovery_expiring_jobs', 'event_recovery_capacity_wait_jobs',
    'workflow_failures', 'workflow_schedule_quota_skips', 'workflow_pending_age_seconds',
    'workflow_waiting_age_seconds', 'workflow_due_age_seconds',
    'pre_auth_pressure', 'edge_validation_failures', 'edge_rejections'
));
