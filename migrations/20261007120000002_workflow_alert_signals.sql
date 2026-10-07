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
    'pre_auth_target_signal_gap_pct', 'workflow_failures', 'workflow_schedule_quota_skips', 'workflow_pending_age_seconds', 'workflow_waiting_age_seconds'
));

ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_workflow_notification_chk
 CHECK (metric NOT IN ('workflow_failures', 'workflow_schedule_quota_skips', 'workflow_pending_age_seconds', 'workflow_waiting_age_seconds') OR action = 'webhook');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('workflow_failures', 'workflow_schedule_quota_skips', 'workflow_pending_age_seconds', 'workflow_waiting_age_seconds')) THEN
  RAISE EXCEPTION 'remove workflow alert rules before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_workflow_notification_chk;
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
