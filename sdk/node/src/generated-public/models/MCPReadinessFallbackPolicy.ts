/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Masked rule configuration for readiness incidents whose delivery has not yet been attempted.
 */
export type MCPReadinessFallbackPolicy = {
  /**
   * Configured fallback provider; omitted when disabled.
   */
  provider?: 'webhook' | 'pagerduty';
  /**
   * Configured PagerDuty fallback region.
   */
  region?: 'us' | 'eu';
  /**
   * Fixed mask for the PagerDuty fallback credential.
   */
  integration_key_masked?: '***';
  /**
   * Monotonic fallback configuration version.
   */
  version: number;
  /**
   * Whether this configuration supplies a fallback to new delivery incidents.
   */
  enabled: boolean;
  /**
   * Configured public fallback address; omitted when disabled.
   */
  webhook_url?: string;
  /**
   * Constant mask for the stored fallback secret; omitted when disabled.
   */
  webhook_secret_sealed_masked?: string;
  /**
   * Configured threshold of completed failed primary dispatches.
   */
  after_failures?: number;
};

