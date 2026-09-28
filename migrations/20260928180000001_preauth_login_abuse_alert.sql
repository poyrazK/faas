-- +goose Up
-- One opt-in, notification-only alert for the aggregate login-target signal.
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_preauth_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_preauth_notification_chk
    CHECK (metric <> 'pre_auth_target_threshold' OR action = 'webhook');

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold'
));

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_category_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_category_chk
    CHECK (category IN ('availability', 'reliability', 'cost', 'deployment', 'infrastructure', 'security'));

INSERT INTO alert_presets (
    name, display_name, description, category, metric, comparison, threshold,
    window_spec, default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES (
    'login_target_pressure',
    'Repeated login target failures',
    'Alerts when more than five approximate login-target threshold events occur in 15 minutes. Requires observe_targets on a central POST route. Notification only; no account lockout.',
    'security', 'pre_auth_target_threshold', 'gt', 5, '15m', 60, true, 'hobby'
) ON CONFLICT (name) DO NOTHING;

-- +goose Down
DELETE FROM alert_presets WHERE name = 'login_target_pressure';

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_category_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_category_chk
    CHECK (category IN ('availability', 'reliability', 'cost', 'deployment', 'infrastructure'));

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_preauth_notification_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));
