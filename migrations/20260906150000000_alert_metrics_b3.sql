-- filename: 20260906150000000_alert_metrics_b3.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1395 B3 — expose durable alert signals that already exist in
-- request_telemetry, app_errors, gateway_queue_depth, and usage_daily.
-- The CHECK constraints remain the database-side closed-set backstop for
-- alert_rules and the system-owned preset catalog.

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

INSERT INTO alert_presets (
    name, display_name, description, category,
    metric, comparison, threshold, window_spec,
    default_cooldown_minutes, enabled_in_catalog, minimum_plan
) VALUES
    (
        'new_error',
        'New error fingerprint',
        'Fires when a previously unseen error fingerprint is recorded for the app during the last 15 minutes.',
        'reliability',
        'new_error_fingerprint', 'gt', 0.0, '15m',
        15, true, 'hobby'
    ),
    (
        'daily_spend_eur_1',
        'Daily usage cost exceeds €1',
        'Fires when the app''s estimated raw RAM usage cost exceeds €1 during the current UTC day. This is a burn-rate signal; monthly included allowances are not subtracted.',
        'cost',
        'daily_cost_cents', 'gt', 100.0, '24h',
        60, true, 'hobby'
    )
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM alert_presets WHERE name IN ('new_error', 'daily_spend_eur_1');

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct',
    'api_up', 'account_spend_eur', 'deployment_failed',
    'cert_expiry_seconds', 'queue_depth',
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
    'canary_stuck_step',
    'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

-- +goose StatementEnd
