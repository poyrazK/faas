-- filename: 20260907100000000_alert_slo_burn_rate.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1398 O2 — expose the ADR-082 API availability SLO as a
-- customer-configurable, multi-window burn-rate preset. The evaluator
-- and Prometheus recording rules both use the Google SRE shape:
-- 14.4x over 1h AND 6x over 6h of the 0.5% error budget.

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count',
    'failed_invocations',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'queue_depth',
    'new_error_fingerprint',
    'cold_wake_rate_pct',
    'daily_cost_cents',
    'slo_burn_rate',
    -- SAFE-RELEASES-OBS PR-B
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct',
    'api_up', 'account_spend_eur', 'deployment_failed',
    'cert_expiry_seconds', 'queue_depth',
    'new_error_fingerprint', 'daily_cost_cents',
    'slo_burn_rate',
    -- SAFE-RELEASES-OBS PR-B
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

INSERT INTO alert_presets (
    name, display_name, description, category,
    metric, comparison, threshold, window_spec,
    default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES (
    'slo_burn_rate',
    'SLO burn',
    'Fires when the app consumes its ADR-082 API-availability error budget at the Google SRE multi-window rate: more than 14.4x over 1 hour and more than 6x over 6 hours of the 99.5% SLO.',
    'reliability',
    'slo_burn_rate', 'gt', 14.4, '6h',
    15, true, 'hobby'
)
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM alert_presets WHERE name = 'slo_burn_rate';

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct',
    'api_up', 'account_spend_eur', 'deployment_failed',
    'cert_expiry_seconds', 'queue_depth',
    'new_error_fingerprint', 'daily_cost_cents',
    -- SAFE-RELEASES-OBS PR-B
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct',
    'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count',
    'failed_invocations',
    'api_up',
    'account_spend_eur',
    'deployment_failed',
    'cert_expiry_seconds',
    'queue_depth',
    'new_error_fingerprint',
    'cold_wake_rate_pct',
    'daily_cost_cents',
    -- SAFE-RELEASES-OBS PR-B
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

-- +goose StatementEnd
