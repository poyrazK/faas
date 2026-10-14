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
   * ADR-1192. Immutable edge rule selector for edge_rule_hits and edge_rule_log_hits; absent means every rule of the app.
   */
  edge_rule_id?: string;
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
  /**
   * MCP task age reads a fresh app gauge in seconds and degrades when unavailable; mcp_tasks_metrics_stale is 1 when either task gauge is missing, stale, or invalid. Min=0 worker apps also require a fresh observer heartbeat with value 1. mcp_task_promotion_hold_age_seconds reads the oldest active hold directly from state (seconds, zero with no holds); its alerts support webhook action only.
   */
  metric: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'mcp_tasks_oldest_age_seconds' | 'mcp_tasks_metrics_stale' | 'mcp_task_promotion_hold_age_seconds' | 'mcp_policy_unconverged_gateways' | 'mcp_policy_unverified' | 'pre_auth_pressure' | 'edge_validation_failures' | 'edge_rejections' | 'edge_waf_detections' | 'event_pending_recipients' | 'event_oldest_pending_seconds' | 'event_retry_rate_per_second' | 'event_terminal_failure_pct' | 'event_routing_latency_p95_seconds' | 'event_paused_seconds' | 'event_drain_rate_per_second' | 'event_execution_dead_letters' | 'event_execution_dead_letter_rate_per_second' | 'event_handler_failure_pct' | 'event_completion_latency_p95_seconds' | 'event_recovery_stalled_jobs' | 'event_recovery_expiring_jobs' | 'event_recovery_capacity_wait_jobs' | 'event_recovery_execution_waiting_jobs' | 'event_recovery_execution_prolonged_wait_jobs' | 'event_recovery_execution_unknown_jobs' | 'event_recovery_execution_retention_risk_jobs' | 'event_recovery_notification_admission_overdue_jobs' | 'event_recovery_notification_admission_dead_jobs' | 'event_recovery_notification_admission_unknown_jobs' | 'event_recovery_notification_admission_no_receivers_jobs' | 'event_recovery_notification_execution_overdue_jobs' | 'event_recovery_notification_execution_dead_jobs' | 'event_recovery_notification_execution_unknown_jobs' | 'event_recovery_notification_execution_no_receivers_jobs' | 'event_retention_expiring_receipts' | 'event_storage_utilization_pct' | 'workflow_failures' | 'workflow_schedule_quota_skips' | 'workflow_pending_age_seconds' | 'workflow_waiting_age_seconds' | 'workflow_due_age_seconds' | 'custom_metric' | 'log_error_lines' | 'log_warn_lines' | 'slo_budget_burn' | 'slo_budget_remaining_pct' | 'synthetic_check_consecutive_failures' | 'synthetic_check_latency_p95_ms' | 'edge_rule_hits' | 'edge_rule_log_hits';
  comparison: 'gt' | 'gte' | 'lt' | 'lte' | 'above_baseline' | 'below_baseline';
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
  /**
   * The customer SLO an slo_budget_burn or slo_budget_remaining_pct rule watches (ADR-747); absent for other metrics.
   */
  slo_id?: string;
  /**
   * The synthetic check a synthetic_check_* rule watches (ADR-748); absent for other metrics.
   */
  synthetic_check_id?: string;
  /**
   * Signed webhook destination; empty for a rule that delivers only to notification channels.
   */
  webhook_url: string;
  /**
   * Notification channels (ADR-749) the rule delivers fires and resolves to.
   */
  channel_ids?: Array<string>;
  /**
   * Literal "***" — the plaintext is never returned.
   */
  webhook_secret_sealed_masked: string;
  cooldown_minutes: number;
  /**
   * What to do when the rule fires. webhook = fire the configured webhook only (legacy default). rollback = roll the rule's app back to its last live deployment. demote = pin the current canary step (no traffic advance). promote = short-circuit the canary ladder to 100%. Pre-auth target and MCP task, event consumer, and workflow metrics support webhook only.
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

