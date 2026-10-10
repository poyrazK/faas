-- +goose Up
-- Three opt-in, notification-only security presets fed by per-app edge
-- signals: pre-auth source-limit pressure, kind=validate mismatches, and
-- rejections by the other edge gates (gateway_edge_rejections_total).
-- Both metrics can be driven by external traffic, so like the login-target
-- metrics they may only send webhooks and never change a deployment.
-- Restates alert_rules_metric_chk and alert_presets_metric_chk, so it must
-- sort after every other migration that restates them (renumbered after
-- 20261010071621984_automation_failure_alert on merging main).
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms',
    'latency_p95_ms',
    'latency_p99_ms',
    'cold_start_pct',
    'request_count',
    'failed_invocations',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'cert_issuance_failed',
    'queue_depth',
    'new_error_fingerprint',
    'cold_wake_rate_pct',
    'daily_cost_cents',
    'slo_burn_rate',
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high',
    'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct',
    'event_pending_recipients',
    'event_oldest_pending_seconds',
    'event_retry_rate_per_second',
    'event_terminal_failure_pct',
    'event_routing_latency_p95_seconds',
    'event_paused_seconds',
    'event_drain_rate_per_second',
    'event_execution_dead_letters',
    'event_execution_dead_letter_rate_per_second',
    'event_handler_failure_pct',
    'event_completion_latency_p95_seconds',
    'event_recovery_stalled_jobs',
    'event_recovery_expiring_jobs',
    'event_recovery_capacity_wait_jobs',
    'event_recovery_execution_waiting_jobs',
    'event_recovery_execution_prolonged_wait_jobs',
    'event_recovery_execution_unknown_jobs',
    'event_recovery_execution_retention_risk_jobs',
    'event_recovery_notification_admission_overdue_jobs',
    'event_recovery_notification_admission_dead_jobs',
    'event_recovery_notification_admission_unknown_jobs',
    'event_recovery_notification_admission_no_receivers_jobs',
    'event_recovery_notification_execution_overdue_jobs',
    'event_recovery_notification_execution_dead_jobs',
    'event_recovery_notification_execution_unknown_jobs',
    'event_recovery_notification_execution_no_receivers_jobs',
    'event_retention_expiring_receipts',
    'event_storage_utilization_pct',
    'workflow_failures',
    'workflow_schedule_quota_skips',
    'workflow_pending_age_seconds',
    'workflow_waiting_age_seconds',
    'workflow_due_age_seconds',
    'pre_auth_pressure',
    'edge_validation_failures',
    'edge_rejections'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_edge_security_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_edge_security_notification_chk
    CHECK (metric NOT IN ('pre_auth_pressure', 'edge_validation_failures', 'edge_rejections') OR action = 'webhook');

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p95_ms',
    'cold_start_pct',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'cert_issuance_failed',
    'queue_depth',
    'new_error_fingerprint',
    'daily_cost_cents',
    'slo_burn_rate',
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high',
    'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct',
    'workflow_due_age_seconds',
    'workflow_failures',
    'pre_auth_pressure',
    'edge_validation_failures',
    'edge_rejections'
));

INSERT INTO alert_presets (
    name, display_name, description, category, metric, comparison, threshold,
    window_spec, default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES (
    'pre_auth_pressure',
    'Pre-auth source limit pressure',
    'Alerts when the pre-auth source limit blocks, or in observe mode would block, more than 100 requests in 15 minutes. Notification only.',
    'security', 'pre_auth_pressure', 'gt', 100, '15m', 60, true, 'hobby'
), (
    'edge_validation_failures',
    'Edge validation failures',
    'Alerts when kind=validate edge rules record more than 50 mismatched requests in 15 minutes, in any validate mode. Notification only.',
    'security', 'edge_validation_failures', 'gt', 50, '15m', 60, true, 'hobby'
), (
    'edge_rejection_pressure',
    'Edge rejection pressure',
    'Alerts when JWT, IP, geo, ingress, body-limit, or throttle edge gates reject more than 200 requests in 15 minutes. Notification only.',
    'security', 'edge_rejections', 'gt', 200, '15m', 60, true, 'hobby'
) ON CONFLICT (name) DO NOTHING;

-- +goose Down
DELETE FROM alert_presets WHERE name IN ('pre_auth_pressure', 'edge_validation_failures', 'edge_rejection_pressure');
DELETE FROM alert_rules WHERE metric IN ('pre_auth_pressure', 'edge_validation_failures', 'edge_rejections');

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p95_ms',
    'cold_start_pct',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'cert_issuance_failed',
    'queue_depth',
    'new_error_fingerprint',
    'daily_cost_cents',
    'slo_burn_rate',
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high',
    'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct',
    'workflow_due_age_seconds',
    'workflow_failures'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_edge_security_notification_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms',
    'latency_p95_ms',
    'latency_p99_ms',
    'cold_start_pct',
    'request_count',
    'failed_invocations',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'cert_issuance_failed',
    'queue_depth',
    'new_error_fingerprint',
    'cold_wake_rate_pct',
    'daily_cost_cents',
    'slo_burn_rate',
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high',
    'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct',
    'event_pending_recipients',
    'event_oldest_pending_seconds',
    'event_retry_rate_per_second',
    'event_terminal_failure_pct',
    'event_routing_latency_p95_seconds',
    'event_paused_seconds',
    'event_drain_rate_per_second',
    'event_execution_dead_letters',
    'event_execution_dead_letter_rate_per_second',
    'event_handler_failure_pct',
    'event_completion_latency_p95_seconds',
    'event_recovery_stalled_jobs',
    'event_recovery_expiring_jobs',
    'event_recovery_capacity_wait_jobs',
    'event_recovery_execution_waiting_jobs',
    'event_recovery_execution_prolonged_wait_jobs',
    'event_recovery_execution_unknown_jobs',
    'event_recovery_execution_retention_risk_jobs',
    'event_recovery_notification_admission_overdue_jobs',
    'event_recovery_notification_admission_dead_jobs',
    'event_recovery_notification_admission_unknown_jobs',
    'event_recovery_notification_admission_no_receivers_jobs',
    'event_recovery_notification_execution_overdue_jobs',
    'event_recovery_notification_execution_dead_jobs',
    'event_recovery_notification_execution_unknown_jobs',
    'event_recovery_notification_execution_no_receivers_jobs',
    'event_retention_expiring_receipts',
    'event_storage_utilization_pct',
    'workflow_failures',
    'workflow_schedule_quota_skips',
    'workflow_pending_age_seconds',
    'workflow_waiting_age_seconds',
    'workflow_due_age_seconds'
));
