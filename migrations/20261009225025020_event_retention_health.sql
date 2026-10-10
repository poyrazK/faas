-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_receipt_retention_hold(p_account uuid, p_outbox bigint, p_accepted timestamptz, p_job_cutoff timestamptz)
RETURNS text LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='running'
  AND p_accepted>=j.from_at AND p_accepted<j.cutoff_at) THEN 'backfill_running'
 WHEN EXISTS (SELECT 1 FROM event_replay_jobs j WHERE j.account_id=p_account AND j.state='completed_with_failures'
  AND j.completed_at>=p_job_cutoff AND EXISTS (SELECT 1 FROM event_replay_job_items i
   WHERE i.job_id=j.id AND i.outbox_id=p_outbox AND i.state='failed' AND i.retryable)) THEN 'backfill_retryable'
 ELSE '' END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk'
  AND strpos(pg_get_constraintdef(oid),'event_retention_expiring_receipts')>0) THEN
 ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_metric_chk;
 ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'event_retention_expiring_receipts'::text, 'event_storage_utilization_pct'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text])));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_event_retention_chk') THEN
 ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_event_retention_chk CHECK (
  metric NOT IN ('event_retention_expiring_receipts','event_storage_utilization_pct') OR
  (app_id IS NOT NULL AND event_subscription_id IS NULL AND action='webhook' AND window_spec IN ('5m','15m','1h','6h','24h')));
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric IN ('event_retention_expiring_receipts','event_storage_utilization_pct')) THEN
 RAISE EXCEPTION 'delete event retention alert rules before rollback'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_event_retention_chk;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((metric = ANY (ARRAY['error_rate_pct'::text, 'latency_p50_ms'::text, 'latency_p95_ms'::text, 'latency_p99_ms'::text, 'cold_start_pct'::text, 'request_count'::text, 'failed_invocations'::text, 'api_up'::text, 'account_spend_eur'::text, 'deployment_failed'::text, 'cert_expiry_seconds'::text, 'cert_issuance_failed'::text, 'queue_depth'::text, 'new_error_fingerprint'::text, 'cold_wake_rate_pct'::text, 'daily_cost_cents'::text, 'slo_burn_rate'::text, 'canary_stuck_step'::text, 'safedeploy_audit_emit_failing'::text, 'deployment_audit_gc_failing'::text, 'canary_fleet_in_flight_high'::text, 'pre_auth_target_threshold'::text, 'pre_auth_target_signal_gap_pct'::text, 'event_pending_recipients'::text, 'event_oldest_pending_seconds'::text, 'event_retry_rate_per_second'::text, 'event_terminal_failure_pct'::text, 'event_routing_latency_p95_seconds'::text, 'event_paused_seconds'::text, 'event_drain_rate_per_second'::text, 'event_execution_dead_letters'::text, 'event_execution_dead_letter_rate_per_second'::text, 'event_handler_failure_pct'::text, 'event_completion_latency_p95_seconds'::text, 'event_recovery_stalled_jobs'::text, 'event_recovery_expiring_jobs'::text, 'event_recovery_capacity_wait_jobs'::text, 'workflow_failures'::text, 'workflow_schedule_quota_skips'::text, 'workflow_pending_age_seconds'::text, 'workflow_waiting_age_seconds'::text, 'workflow_due_age_seconds'::text])));
DROP FUNCTION IF EXISTS event_receipt_retention_hold(uuid,bigint,timestamptz,timestamptz);
