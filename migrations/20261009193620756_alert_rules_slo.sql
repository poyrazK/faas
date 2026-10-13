-- +goose Up
-- ADR-747 slice 3: alert rules on a customer SLO. slo_id is set exactly for
-- the two SLO metrics, which are app-scoped; deleting the SLO deletes them.
-- +goose StatementBegin
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS slo_id uuid REFERENCES app_slos(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS alert_rules_slo_id_idx ON alert_rules (slo_id) WHERE slo_id IS NOT NULL;
DO $$
BEGIN
    -- Preserve a later migration's broader contract when replaying this one.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk' AND strpos(pg_get_constraintdef(oid), 'slo_budget_burn') > 0) THEN
        ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text, 'slo_budget_burn'::text, 'slo_budget_remaining_pct'::text])));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_slo_chk') THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_slo_chk CHECK ((((metric = ANY (ARRAY['slo_budget_burn'::text, 'slo_budget_remaining_pct'::text])) = (slo_id IS NOT NULL)) AND ((slo_id IS NULL) OR (app_id IS NOT NULL))));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM alert_rules WHERE slo_id IS NOT NULL) THEN RAISE EXCEPTION 'delete SLO alert rules before rollback'; END IF; END $$;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_slo_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text])));
DROP INDEX IF EXISTS alert_rules_slo_id_idx;
ALTER TABLE alert_rules DROP COLUMN slo_id;
-- +goose StatementEnd
