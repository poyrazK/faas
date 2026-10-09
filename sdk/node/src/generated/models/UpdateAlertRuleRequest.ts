/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update — every field is optional. Omitted means leave alone.
 */
export type UpdateAlertRuleRequest = {
  /**
   * Change the completed-release recovery window. Omission preserves it and explicit 0 disables it. Positive values require action=rollback; only error_rate_pct with gt or gte and sufficient exact deployment telemetry qualifies.
   */
  post_deploy_rollback_window_seconds?: number;
  name?: string;
  enabled?: boolean;
  /**
   * Cannot cross metric families (e.g. error_rate_pct → failed_invocations) — returns 400.
   */
  metric?: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'event_pending_recipients' | 'event_oldest_pending_seconds' | 'event_retry_rate_per_second' | 'event_terminal_failure_pct' | 'event_routing_latency_p95_seconds' | 'event_paused_seconds' | 'event_drain_rate_per_second' | 'event_execution_dead_letters' | 'event_execution_dead_letter_rate_per_second' | 'event_handler_failure_pct' | 'event_completion_latency_p95_seconds' | 'event_recovery_stalled_jobs' | 'event_recovery_expiring_jobs' | 'event_recovery_capacity_wait_jobs' | 'event_retention_expiring_receipts' | 'event_storage_utilization_pct';
  comparison?: 'gt' | 'gte' | 'lt' | 'lte';
  threshold?: number;
  window_spec?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  webhook_url?: string;
  /**
   * New plaintext HMAC secret. Omit to keep the existing secret.
   */
  webhook_secret?: string;
  cooldown_minutes?: number;
  /**
   * Replace the action. Omit to leave the existing action in place. Pre-auth target and event consumer and workflow metrics support webhook only.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

