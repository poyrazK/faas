-- ADR-829: opt-in notification policy using the existing terminal workflow failure signal.
-- +goose Up
ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p95_ms'::text, 'cold_start_pct'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'workflow_due_age_seconds'::text, 'workflow_failures'::text])));
INSERT INTO alert_presets (
 name,display_name,description,category,metric,comparison,threshold,
 window_spec,default_cooldown_minutes,enabled_in_catalog,minimum_plan
) VALUES (
 'automation_failures','Automation run failed',
 'Notifies when at least one automation run fails or dies in the last five minutes. Excludes cancellations and intermediate retry attempts. Covers all workflows in the selected app. Webhook only; default cooldown is thirty minutes.',
 'reliability','workflow_failures','gte',1,'5m',30,true,'hobby'
) ON CONFLICT(name) DO NOTHING;

-- +goose Down
DELETE FROM alert_presets WHERE name='automation_failures';
ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p95_ms'::text, 'cold_start_pct'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'workflow_due_age_seconds'::text])));
