/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an alert rule on an app.
 */
export type CreateAlertRuleRequest = {
  /**
   * Event subscription id in the create alert rule request: immutable subscription selector. Required only for event consumer metrics; webhook action and windows up to 24h are required.
   */
  event_subscription_id?: string;
  /**
   * Enable completed-release rollback for this many seconds after cutover; 0 disables it. Requires action=rollback. Only deployment-specific error_rate_pct breaches with gt or gte comparisons can qualify.
   */
  post_deploy_rollback_window_seconds?: number;
  name: string;
  enabled?: boolean;
  metric: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'cert_issuance_failed' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents' | 'slo_burn_rate' | 'pre_auth_target_threshold' | 'pre_auth_target_signal_gap_pct' | 'pre_auth_pressure' | 'edge_validation_failures' | 'edge_rejections' | 'edge_waf_detections' | 'event_pending_recipients' | 'event_oldest_pending_seconds' | 'event_retry_rate_per_second' | 'event_terminal_failure_pct' | 'event_routing_latency_p95_seconds' | 'event_paused_seconds' | 'event_drain_rate_per_second' | 'event_execution_dead_letters' | 'event_execution_dead_letter_rate_per_second' | 'event_handler_failure_pct' | 'event_completion_latency_p95_seconds' | 'event_recovery_stalled_jobs' | 'event_recovery_expiring_jobs' | 'event_recovery_capacity_wait_jobs' | 'event_recovery_execution_waiting_jobs' | 'event_recovery_execution_prolonged_wait_jobs' | 'event_recovery_execution_unknown_jobs' | 'event_recovery_execution_retention_risk_jobs' | 'event_recovery_notification_admission_overdue_jobs' | 'event_recovery_notification_admission_dead_jobs' | 'event_recovery_notification_admission_unknown_jobs' | 'event_recovery_notification_admission_no_receivers_jobs' | 'event_recovery_notification_execution_overdue_jobs' | 'event_recovery_notification_execution_dead_jobs' | 'event_recovery_notification_execution_unknown_jobs' | 'event_recovery_notification_execution_no_receivers_jobs' | 'event_retention_expiring_receipts' | 'event_storage_utilization_pct';
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
   * What to do when the rule fires. Omit to default to webhook. Pre-auth target and event consumer and workflow metrics support webhook only.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

