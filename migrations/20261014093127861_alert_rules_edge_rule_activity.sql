-- filename: 20261014093127861_alert_rules_edge_rule_activity.sql
--
-- ADR-1192: alert rules on edge-rule activity. Two metrics count the ADR-960
-- hourly hit buckets: edge_rule_hits (enforced matches) and
-- edge_rule_log_hits (log-mode matches). edge_rule_id optionally scopes a
-- rule to one edge rule; it is deliberately not a foreign key because an
-- ADR-961 rule-set restore deletes and re-inserts rules under the same ids.
-- Edge-rule metrics are app-scoped, webhook-only and limited to the windows
-- hourly buckets with 14-day retention can answer.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS edge_rule_id uuid;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_metric_chk'
          AND strpos(pg_get_constraintdef(oid), 'edge_rule_hits') > 0
    ) THEN
        ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'event_recovery_execution_waiting_jobs'::text, 'event_recovery_execution_prolonged_wait_jobs'::text, 'event_recovery_execution_unknown_jobs'::text, 'event_recovery_execution_retention_risk_jobs'::text, 'event_recovery_notification_admission_overdue_jobs'::text, 'event_recovery_notification_admission_dead_jobs'::text, 'event_recovery_notification_admission_unknown_jobs'::text, 'event_recovery_notification_admission_no_receivers_jobs'::text, 'event_recovery_notification_execution_overdue_jobs'::text, 'event_recovery_notification_execution_dead_jobs'::text, 'event_recovery_notification_execution_unknown_jobs'::text, 'event_recovery_notification_execution_no_receivers_jobs'::text, 'event_retention_expiring_receipts'::text, 'event_storage_utilization_pct'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text, 'pre_auth_pressure'::text, 'edge_validation_failures'::text, 'edge_rejections'::text, 'edge_waf_detections'::text, 'mcp_tasks_oldest_age_seconds'::text, 'mcp_tasks_metrics_stale'::text, 'mcp_task_promotion_hold_age_seconds'::text, 'edge_rule_hits'::text, 'edge_rule_log_hits'::text])));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_edge_rule_chk'
    ) THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_edge_rule_chk CHECK (
            (metric IN ('edge_rule_hits','edge_rule_log_hits') AND app_id IS NOT NULL AND event_subscription_id IS NULL
                AND action = 'webhook' AND window_spec IN ('1h', '6h', '24h', '7d'))
            OR (metric NOT IN ('edge_rule_hits','edge_rule_log_hits') AND edge_rule_id IS NULL)
        );
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS alert_rules_edge_rule_idx
    ON alert_rules (edge_rule_id) WHERE edge_rule_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('edge_rule_hits','edge_rule_log_hits')) THEN
        RAISE EXCEPTION 'cannot roll back: edge-rule activity alert rules exist';
    END IF;
END
$$;
DROP INDEX IF EXISTS alert_rules_edge_rule_idx;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_edge_rule_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'event_recovery_execution_waiting_jobs'::text, 'event_recovery_execution_prolonged_wait_jobs'::text, 'event_recovery_execution_unknown_jobs'::text, 'event_recovery_execution_retention_risk_jobs'::text, 'event_recovery_notification_admission_overdue_jobs'::text, 'event_recovery_notification_admission_dead_jobs'::text, 'event_recovery_notification_admission_unknown_jobs'::text, 'event_recovery_notification_admission_no_receivers_jobs'::text, 'event_recovery_notification_execution_overdue_jobs'::text, 'event_recovery_notification_execution_dead_jobs'::text, 'event_recovery_notification_execution_unknown_jobs'::text, 'event_recovery_notification_execution_no_receivers_jobs'::text, 'event_retention_expiring_receipts'::text, 'event_storage_utilization_pct'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text, 'pre_auth_pressure'::text, 'edge_validation_failures'::text, 'edge_rejections'::text, 'edge_waf_detections'::text, 'mcp_tasks_oldest_age_seconds'::text, 'mcp_tasks_metrics_stale'::text, 'mcp_task_promotion_hold_age_seconds'::text])));
ALTER TABLE alert_rules DROP COLUMN IF EXISTS edge_rule_id;
-- +goose StatementEnd
