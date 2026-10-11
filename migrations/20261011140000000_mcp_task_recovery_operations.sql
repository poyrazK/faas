-- +goose Up
ALTER TABLE deployment_mcp_task_holds
    ADD COLUMN last_recovery_outcome text NOT NULL DEFAULT '' CHECK (last_recovery_outcome IN ('', 'held', 'retry', 'recovered', 'inactive', 'cancelled')),
    ADD COLUMN last_recovery_at timestamptz,
    ADD COLUMN last_recovery_error_code text NOT NULL DEFAULT '' CHECK (length(last_recovery_error_code) <= 128);
ALTER TABLE deployment_audit DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK (kind IN ('deploy.created', 'deploy.source_ref', 'deploy.local_tarball', 'deploy.traffic_changed', 'deploy.health_probe_failed', 'deploy.health_recovered', 'deploy.rolled_back', 'deploy.removed', 'deploy.rollout_started', 'deploy.rollout_completed', 'deploy.rollout_aborted', 'deploy.canary_step_advanced', 'deploy.alert_rule_fired', 'deploy.scan_regressed', 'deploy.mcp_task_held', 'deploy.mcp_task_retry_requested', 'deploy.mcp_task_retry_started', 'deploy.mcp_task_retry_completed', 'deploy.mcp_task_recovered', 'deploy.mcp_task_hold_cancelled'));
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN ('error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms', 'cold_start_pct', 'request_count', 'failed_invocations', 'api_up', 'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds', 'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint', 'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step', 'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing', 'canary_fleet_in_flight_high', 'pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct', 'mcp_tasks_oldest_age_seconds', 'mcp_tasks_metrics_stale', 'mcp_task_promotion_hold_age_seconds'));
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_mcp_tasks_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_tasks_notification_chk CHECK (metric NOT IN ('mcp_tasks_oldest_age_seconds','mcp_tasks_metrics_stale','mcp_task_promotion_hold_age_seconds') OR action='webhook');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM deployment_audit WHERE kind IN ('deploy.mcp_task_held', 'deploy.mcp_task_retry_requested', 'deploy.mcp_task_retry_started', 'deploy.mcp_task_retry_completed', 'deploy.mcp_task_recovered', 'deploy.mcp_task_hold_cancelled')) OR
       EXISTS (SELECT 1 FROM alert_rules WHERE metric='mcp_task_promotion_hold_age_seconds') THEN
        RAISE EXCEPTION 'retain MCP recovery audit events and remove hold-age alert rules before rollback';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE deployment_audit DROP CONSTRAINT IF EXISTS deployment_audit_kind_chk;
ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK (kind IN ('deploy.created', 'deploy.source_ref', 'deploy.local_tarball', 'deploy.traffic_changed', 'deploy.health_probe_failed', 'deploy.health_recovered', 'deploy.rolled_back', 'deploy.removed', 'deploy.rollout_started', 'deploy.rollout_completed', 'deploy.rollout_aborted', 'deploy.canary_step_advanced', 'deploy.alert_rule_fired', 'deploy.scan_regressed'));
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK (metric IN ('error_rate_pct', 'latency_p50_ms', 'latency_p95_ms', 'latency_p99_ms', 'cold_start_pct', 'request_count', 'failed_invocations', 'api_up', 'account_spend_eur', 'deployment_failed', 'cert_expiry_seconds', 'cert_issuance_failed', 'queue_depth', 'new_error_fingerprint', 'cold_wake_rate_pct', 'daily_cost_cents', 'slo_burn_rate', 'canary_stuck_step', 'safedeploy_audit_emit_failing', 'deployment_audit_gc_failing', 'canary_fleet_in_flight_high', 'pre_auth_target_threshold', 'pre_auth_target_signal_gap_pct', 'mcp_tasks_oldest_age_seconds', 'mcp_tasks_metrics_stale'));
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_mcp_tasks_notification_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_tasks_notification_chk CHECK (metric NOT IN ('mcp_tasks_oldest_age_seconds','mcp_tasks_metrics_stale') OR action='webhook');
ALTER TABLE deployment_mcp_task_holds DROP COLUMN last_recovery_outcome, DROP COLUMN last_recovery_at, DROP COLUMN last_recovery_error_code;
