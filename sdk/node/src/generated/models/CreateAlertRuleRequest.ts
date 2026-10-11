/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an alert rule on an app.
 */
export type CreateAlertRuleRequest = {
  /**
   * Enable completed-release rollback for this many seconds after cutover; 0 disables it. Requires action=rollback. Only deployment-specific error_rate_pct breaches with gt or gte comparisons can qualify.
   */
  post_deploy_rollback_window_seconds?: number;
  name: string;
  enabled?: boolean;
  /**
   * MCP task age reads a fresh app gauge in seconds and degrades when unavailable; mcp_tasks_metrics_stale is 1 when either task gauge is missing, stale, or invalid. Min=0 worker apps also require a fresh observer heartbeat with value 1. mcp_task_promotion_hold_age_seconds reads the oldest active hold directly from state (seconds, zero with no holds); its alerts support webhook action only.
   */
  metric: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'mcp_tasks_oldest_age_seconds' | 'mcp_tasks_metrics_stale' | 'mcp_task_promotion_hold_age_seconds';
  comparison: 'gt' | 'gte' | 'lt' | 'lte';
  threshold: number;
  window_spec: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Required when metric == failed_invocations; omit otherwise (xor_chk).
   */
  failure_source?: 'any' | 'cron' | 'queue' | 'delayed_task' | 'async_invoke' | 'inbound_webhook';
  webhook_url: string;
  /**
   * Plaintext HMAC secret (max 256 bytes). Sealed at rest; never echoed.
   */
  webhook_secret: string;
  cooldown_minutes?: number;
  /**
   * What to do when the rule fires. Omit to default to webhook. Pre-auth target and MCP task metrics support webhook only.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

