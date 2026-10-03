-- +goose Up
-- A coverage alert needs an explicit unknown state for windows without a
-- meaningful sample. It remains notification-only like target pressure.
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_preauth_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_preauth_notification_chk
    CHECK (metric NOT IN ('pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct') OR action = 'webhook');

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_state_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_state_chk
    CHECK (state IN ('ok', 'firing', 'degraded', 'unknown'));

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold',
    'pre_auth_target_signal_gap_pct'
));

INSERT INTO alert_presets (
    name, display_name, description, category, metric, comparison, threshold,
    window_spec, default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES (
    'login_target_signal_health',
    'Login target signal coverage',
    'Alerts when more than 10% of selected login failures on any observed route have missing or invalid target digests, with at least 20 failures in 15 minutes. Notification only.',
    'security', 'pre_auth_target_signal_gap_pct', 'gt', 10, '15m', 60, true, 'hobby'
) ON CONFLICT (name) DO NOTHING;

-- +goose Down
-- Existing customer rules using this metric must be removed explicitly before
-- rollback; silently deleting their webhook configuration would lose data.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'pre_auth_target_signal_gap_pct') THEN
        RAISE EXCEPTION 'remove pre_auth_target_signal_gap_pct alert rules before rollback';
    END IF;
END $$;
-- +goose StatementEnd

DELETE FROM alert_presets WHERE name = 'login_target_signal_health';
UPDATE alert_rules SET state = 'degraded' WHERE state = 'unknown';

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high', 'pre_auth_target_threshold'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_state_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_state_chk
    CHECK (state IN ('ok', 'firing', 'degraded'));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_preauth_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_preauth_notification_chk
    CHECK (metric <> 'pre_auth_target_threshold' OR action = 'webhook');

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
