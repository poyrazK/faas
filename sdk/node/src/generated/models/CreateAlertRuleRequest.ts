/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an alert rule on an app.
 */
export type CreateAlertRuleRequest = {
  /**
   * Readiness environment requested when creating this alert. Immutable readiness environment selector. Omitted or empty means every environment; default selects only default. Valid only for mcp_readiness_unhealthy.
   */
  mcp_readiness_scope?: string;
  /**
   * Observation minimum requested for the new protocol alert. MCP protocol alerts only. Minimum observations in every rolling window; defaults to 20 on create. Latency uses measured executions. Omit for other metrics.
   */
  mcp_min_calls?: number;
  /**
   * Sustained duration requested for the new protocol alert. MCP protocol alerts only. Continuous breach duration sampled every 30 seconds; defaults to 10 on create. Missing observations interrupt eligibility. Webhook action, app scope, gt/gte and windows 5m/15m/1h are required. Error thresholds are percent (0..100), execution p95 thresholds are milliseconds (0..300000).
   */
  mcp_for_minutes?: number;
  /**
   * Event subscription id in the create alert rule request: immutable subscription selector. Required only for event consumer metrics; webhook action and windows up to 24h are required.
   */
  event_subscription_id?: string;
  /**
   * Edge rule to count for edge_rule_hits (enforced matches), edge_rule_log_hits (log-mode matches) or edge_rule_activity (matches in either mode, so the alert survives a switch from log mode to enforce); omit to count every rule of the app. Immutable. Edge-rule metrics are webhook-only and take window_spec 1h, 6h, 24h or 7d (hourly hit buckets, kept 14 days).
   */
  edge_rule_id?: string;
  /**
   * Enable completed-release rollback for this many seconds after cutover; 0 disables it. Requires action=rollback. Only deployment-specific error_rate_pct breaches with gt or gte comparisons can qualify.
   */
  post_deploy_rollback_window_seconds?: number;
  name: string;
  enabled?: boolean;
  /**
   * Selected metric for the existing alert rule. MCP task age reads a fresh app gauge in seconds and degrades when unavailable; mcp_tasks_metrics_stale is 1 when either task gauge is missing, stale, or invalid. Min=0 worker apps also require a fresh observer heartbeat with value 1. mcp_task_promotion_hold_age_seconds reads the oldest active hold directly from state (seconds, zero with no holds); its alerts support webhook action only.
   */
  metric: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'pre_auth_pressure' | 'edge_validation_failures' | 'edge_rejections' | 'edge_waf_detections' | 'event_pending_recipients' | 'event_oldest_pending_seconds' | 'event_retry_rate_per_second' | 'event_terminal_failure_pct' | 'event_routing_latency_p95_seconds' | 'event_paused_seconds' | 'event_drain_rate_per_second' | 'event_execution_dead_letters' | 'event_execution_dead_letter_rate_per_second' | 'event_handler_failure_pct' | 'event_completion_latency_p95_seconds' | 'event_recovery_stalled_jobs' | 'event_recovery_expiring_jobs' | 'event_recovery_capacity_wait_jobs' | 'event_recovery_execution_waiting_jobs' | 'event_recovery_execution_prolonged_wait_jobs' | 'event_recovery_execution_unknown_jobs' | 'event_recovery_execution_retention_risk_jobs' | 'event_recovery_notification_admission_overdue_jobs' | 'event_recovery_notification_admission_dead_jobs' | 'event_recovery_notification_admission_unknown_jobs' | 'event_recovery_notification_admission_no_receivers_jobs' | 'event_recovery_notification_execution_overdue_jobs' | 'event_recovery_notification_execution_dead_jobs' | 'event_recovery_notification_execution_unknown_jobs' | 'event_recovery_notification_execution_no_receivers_jobs' | 'event_retention_expiring_receipts' | 'event_storage_utilization_pct' | 'mcp_tasks_oldest_age_seconds' | 'mcp_tasks_metrics_stale' | 'mcp_task_promotion_hold_age_seconds' | 'mcp_policy_unconverged_gateways' | 'mcp_policy_unverified' | 'mcp_readiness_unhealthy' | 'mcp_catalog_drift' | 'mcp_catalog_monitor_unavailable' | 'mcp_oauth_reauthorization_required' | 'mcp_oauth_renewal_failed' | 'mcp_oauth_retirement_expiring' | 'mcp_oauth_issuance_unknown' | 'mcp_protocol_error_rate_pct' | 'mcp_protocol_execution_p95_ms' | 'edge_rule_hits' | 'edge_rule_log_hits' | 'custom_metric' | 'log_error_lines' | 'log_warn_lines' | 'slo_budget_burn' | 'slo_budget_remaining_pct' | 'synthetic_check_consecutive_failures' | 'synthetic_check_latency_p95_ms' | 'edge_rule_activity' | 'route_insights_detected' | 'route_monitor_incidents' | 'route_health_holds' | 'log_search_matches';
  /**
   * above_baseline and below_baseline (preview behind FAAS_ANOMALY_ALERTS_ENABLED) compare against the median of the same metric at the same time of day over the previous 7 days; threshold is then a multiplier (1.5-20 above, 0.05-0.67 below). Supported for error_rate_pct, latency_p95_ms, latency_p99_ms, cold_start_pct and request_count (below_baseline: request_count only).
   */
  comparison: 'gt' | 'gte' | 'lt' | 'lte' | 'above_baseline' | 'below_baseline';
  threshold: number;
  window_spec: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Required when metric == failed_invocations; omit otherwise (xor_chk).
   */
  failure_source?: 'any' | 'cron' | 'queue' | 'delayed_task' | 'async_invoke' | 'inbound_webhook';
  /**
   * Required when metric is custom_metric; omit otherwise. A gauge is compared on its average over the window and a counter on its per-second rate. custom_metric rules support webhook action only and windows up to 24h.
   */
  custom_metric_name?: string;
  /**
   * Required when metric is log_search_matches; omit otherwise. URL query string of log search filters (source, level, status, route, deployment_id, request_id, trace_id, q); the rule counts matching log events in its window. Webhook action only, windows up to 24h.
   */
  log_query?: string;
  /**
   * Required for slo_budget_burn (multi-window burn rate; 14.4 is the paging threshold) and slo_budget_remaining_pct (percent of the error budget left); names one of this app's SLOs. The rule window_spec is ignored for these metrics.
   */
  slo_id?: string;
  /**
   * Required for synthetic_check_consecutive_failures (failed runs since the last success; window_spec ignored) and synthetic_check_latency_p95_ms (p95 of successful runs in the window); names one of this app's checks.
   */
  synthetic_check_id?: string;
  /**
   * Signed webhook destination. Required with webhook_secret unless channel_ids is set.
   */
  webhook_url?: string;
  /**
   * Notification channels to deliver fires and resolves to. With at least one, webhook_url and webhook_secret may be omitted.
   */
  channel_ids?: Array<string>;
  /**
   * Plaintext HMAC secret (max 256 bytes). Sealed at rest; never echoed.
   */
  webhook_secret?: string;
  cooldown_minutes?: number;
  /**
   * What to do when the rule fires. Omit to default to webhook. Pre-auth target and MCP task, event consumer, and workflow metrics support webhook only.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

