/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update — every field is optional. Omitted means leave alone.
 */
export type UpdateAlertRuleRequest = {
  name?: string;
  enabled?: boolean;
  /**
   * Cannot cross metric families (e.g. error_rate_pct → failed_invocations) — returns 400.
   */
  metric?: 'error_rate_pct' | 'latency_p50_ms' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count' | 'failed_invocations' | 'api_up' | 'account_spend_eur' | 'deployment_failed' | 'cert_expiry_seconds' | 'queue_depth' | 'new_error_fingerprint' | 'cold_wake_rate_pct' | 'daily_cost_cents';
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
   * Replace the action. Omit to leave the existing action in place.
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
};

