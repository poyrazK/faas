/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update — every field is optional. Omitted means leave alone.
 */
export type UpdateAlertRuleRequest = {
  /**
   * Immutable environment selector. If supplied it must equal the current value; create a new rule to select another environment.
   */
  mcp_readiness_scope?: string;
  /**
   * Replacement observation minimum for the existing protocol alert. MCP protocol alerts only. Minimum observations in every rolling window; defaults to 20 on create. Latency uses measured executions. Omit for other metrics.
   */
  mcp_min_calls?: number;
  /**
   * Replacement sustained duration for the existing protocol alert. MCP protocol alerts only. Continuous breach duration sampled every 30 seconds; defaults to 10 on create. Missing observations interrupt eligibility. Webhook action, app scope, gt/gte and windows 5m/15m/1h are required. Error thresholds are percent (0..100), execution p95 thresholds are milliseconds (0..300000).
   */
  mcp_for_minutes?: number;
  /**
   * Change the completed-release recovery window. Omission preserves it and explicit 0 disables it. Positive values require action=rollback; only error_rate_pct with gt or gte and sufficient exact deployment telemetry qualifies.
   */
  post_deploy_rollback_window_seconds?: number;
  name?: string;
  enabled?: boolean;
  /**
   * Cannot cross metric families (e.g. error_rate_pct → failed_invocations) — returns 400. MCP task metrics are notification-only.
   */
  metric?: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'pre_auth_pressure' | 'edge_validation_failures' | 'edge_rejections' | 'edge_waf_detections' | 'event_pending_recipients' | 'event_oldest_pending_seconds' | 'event_retry_rate_per_second' | 'event_terminal_failure_pct' | 'event_routing_latency_p95_seconds' | 'event_paused_seconds' | 'event_drain_rate_per_second' | 'event_execution_dead_letters' | 'event_execution_dead_letter_rate_per_second' | 'event_handler_failure_pct' | 'event_completion_latency_p95_seconds' | 'event_recovery_stalled_jobs' | 'event_recovery_expiring_jobs' | 'event_recovery_capacity_wait_jobs' | 'event_recovery_execution_waiting_jobs' | 'event_recovery_execution_prolonged_wait_jobs' | 'event_recovery_execution_unknown_jobs' | 'event_recovery_execution_retention_risk_jobs' | 'event_recovery_notification_admission_overdue_jobs' | 'event_recovery_notification_admission_dead_jobs' | 'event_recovery_notification_admission_unknown_jobs' | 'event_recovery_notification_admission_no_receivers_jobs' | 'event_recovery_notification_execution_overdue_jobs' | 'event_recovery_notification_execution_dead_jobs' | 'event_recovery_notification_execution_unknown_jobs' | 'event_recovery_notification_execution_no_receivers_jobs' | 'event_retention_expiring_receipts' | 'event_storage_utilization_pct' | 'mcp_tasks_oldest_age_seconds' | 'mcp_tasks_metrics_stale' | 'mcp_task_promotion_hold_age_seconds' | 'mcp_policy_unconverged_gateways' | 'mcp_policy_unverified' | 'mcp_readiness_unhealthy' | 'mcp_catalog_drift' | 'mcp_catalog_monitor_unavailable' | 'mcp_oauth_reauthorization_required' | 'mcp_oauth_renewal_failed' | 'mcp_oauth_retirement_expiring' | 'mcp_oauth_issuance_unknown' | 'mcp_protocol_error_rate_pct' | 'mcp_protocol_execution_p95_ms' | 'edge_rule_hits' | 'edge_rule_log_hits' | 'custom_metric' | 'log_error_lines' | 'log_warn_lines' | 'slo_budget_burn' | 'slo_budget_remaining_pct' | 'synthetic_check_consecutive_failures' | 'synthetic_check_latency_p95_ms';
  comparison?: 'gt' | 'gte' | 'lt' | 'lte' | 'above_baseline' | 'below_baseline';
  threshold?: number;
  window_spec?: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  webhook_url?: string;
  /**
   * Replaces the rule's notification channels; an empty list clears them, which a rule without a webhook cannot do.
   */
  channel_ids?: Array<string>;
  /**
   * New plaintext HMAC secret. Omit to keep the existing secret.
   */
  webhook_secret?: string;
  cooldown_minutes?: number;
  /**
   * Replace the action. Omit to leave the existing action in place. Pre-auth target and MCP task, event consumer, and workflow metrics support webhook only.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

