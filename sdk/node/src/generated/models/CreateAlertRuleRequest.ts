/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an alert rule on an app.
 */
export type CreateAlertRuleRequest = {
  name: string;
  enabled?: boolean;
  metric: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents';
  comparison: 'gt' | 'gte' | 'lt' | 'lte';
  threshold: number;
  window_spec: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Required when metric == failed_invocations; omit otherwise (xor_chk).
   */
  failure_source?: 'any' | 'cron' | 'queue' | 'delayed_task' | 'async_invoke';
  webhook_url: string;
  /**
   * Plaintext HMAC secret (max 256 bytes). Sealed at rest; never echoed.
   */
  webhook_secret: string;
  cooldown_minutes?: number;
  /**
   * What to do when the rule fires. Omit to default to webhook.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

