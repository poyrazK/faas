/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A customer-configurable alert rule. Carries the masked webhook
 * secret; the sealed ciphertext is server-side only.
 *
 */
export type AlertRuleResponse = {
  /**
   * Immutable subscription selector. Required only for event consumer metrics; webhook action and windows up to 24h are required.
   */
  event_subscription_id?: string;
  /**
   * Configured completed-release rollback eligibility window in seconds; 0 is disabled. Acceptance requires deployment-specific error_rate_pct evidence, a gt or gte comparison, and recorded predecessor lineage.
   */
  post_deploy_rollback_window_seconds?: number;
  id: string;
  /**
   * Pinned app id. Empty string = account-wide rule.
   */
  app_id: string;
  name: string;
  enabled: boolean;
  metric: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'event_pending_recipients' | 'event_oldest_pending_seconds' | 'event_retry_rate_per_second' | 'event_terminal_failure_pct' | 'event_routing_latency_p95_seconds' | 'event_paused_seconds' | 'event_drain_rate_per_second' | 'event_execution_dead_letters' | 'event_execution_dead_letter_rate_per_second' | 'event_handler_failure_pct' | 'event_completion_latency_p95_seconds' | 'event_recovery_stalled_jobs' | 'event_recovery_expiring_jobs' | 'event_recovery_capacity_wait_jobs' | 'workflow_failures' | 'workflow_schedule_quota_skips' | 'workflow_pending_age_seconds' | 'workflow_waiting_age_seconds' | 'workflow_due_age_seconds' | 'custom_metric';
  comparison: 'gt' | 'gte' | 'lt' | 'lte';
  threshold: number;
  window_spec: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Source dimension for failed_invocations; omit when metric is not failed_invocations (xor_chk).
   */
  failure_source?: 'any' | 'cron' | 'queue' | 'delayed_task' | 'async_invoke' | 'inbound_webhook';
  /**
   * The pushed custom metric a custom_metric rule watches (ADR-745). Present only when metric is custom_metric.
   */
  custom_metric_name?: string;
  webhook_url: string;
  /**
   * Literal "***" — the plaintext is never returned.
   */
  webhook_secret_sealed_masked: string;
  cooldown_minutes: number;
  /**
   * What to do when the rule fires. webhook = fire the configured webhook only (legacy default). rollback = roll the rule's app back to its last live deployment. demote = pin the current canary step (no traffic advance). promote = short-circuit the canary ladder to 100%. Pre-auth target and event consumer and workflow metrics support webhook only.
   */
  action: 'webhook' | 'rollback' | 'demote' | 'promote';
  /**
   * Evaluation state. degraded means the rule's own metric source is unavailable; unknown means insufficient samples, compacted event history, or a paused event consumer.
   */
  state: 'ok' | 'firing' | 'degraded' | 'unknown';
  last_fired_at?: string;
  last_evaluated_at?: string;
  created_at: string;
  updated_at: string;
};

