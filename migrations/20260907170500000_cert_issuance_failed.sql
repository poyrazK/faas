-- filename: 20260907170500000_cert_issuance_failed.sql
-- +goose Up

-- F2 (issue #1397): retain the start of the current certificate failure
-- episode and the last notification stamp. The former is distinct from
-- dns_last_checked_at, which the doctor refreshes every pass; the latter is
-- the durable per-domain 24-hour email cooldown.
ALTER TABLE custom_domains
    ADD COLUMN IF NOT EXISTS cert_failed_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_cert_issuance_failed_email_at timestamptz;

CREATE INDEX IF NOT EXISTS custom_domains_cert_failed_idx
    ON custom_domains (cert_failed_at)
    WHERE cert_status = 'failed';

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'cold_wake_rate_pct', 'daily_cost_cents', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint',
    'daily_cost_cents', 'canary_stuck_step',
    'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing',
    'canary_fleet_in_flight_high'
));

-- +goose Down

ALTER TABLE alert_presets DROP CONSTRAINT IF EXISTS alert_presets_metric_chk;
ALTER TABLE alert_presets ADD CONSTRAINT alert_presets_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p95_ms', 'cold_start_pct', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'queue_depth', 'new_error_fingerprint', 'daily_cost_cents',
    'canary_stuck_step', 'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing', 'canary_fleet_in_flight_high'
));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN (
    'error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms',
    'cold_start_pct', 'request_count', 'failed_invocations', 'api_up',
    'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds',
    'queue_depth', 'new_error_fingerprint', 'cold_wake_rate_pct',
    'daily_cost_cents', 'canary_stuck_step', 'safedeploy_audit_emit_failing',
    'deployment_audit_gc_failing', 'canary_fleet_in_flight_high'
));

DROP INDEX IF EXISTS custom_domains_cert_failed_idx;
ALTER TABLE custom_domains
    DROP COLUMN IF EXISTS last_cert_issuance_failed_email_at,
    DROP COLUMN IF EXISTS cert_failed_at;
