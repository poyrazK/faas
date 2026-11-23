/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optimistically versioned fallback replacement. Webhook requires webhook_url and webhook_secret. PagerDuty requires provider=pagerduty and integration_key with optional region. Enabled policies require after_failures; disabling omits all destination fields.
 */
export type SetMCPReadinessFallbackRequest = {
  /**
   * Destination provider; omitted means webhook. Omit when disabling.
   */
  provider?: 'webhook' | 'pagerduty';
  /**
   * PagerDuty region; defaults to US. Omit for webhook or disabled policies.
   */
  region?: 'us' | 'eu';
  /**
   * PagerDuty fallback Events API key; omit webhook fields when selecting this provider.
   */
  integration_key?: string;
  /**
   * Current fallback policy version; zero is valid for an unconfigured rule.
   */
  expected_version: number;
  /**
   * Whether unclaimed incidents should acquire a fallback destination.
   */
  enabled: boolean;
  /**
   * Public webhook fallback receiver URL; required for webhook only and must differ from the primary webhook address.
   */
  webhook_url?: string;
  /**
   * Fallback HMAC signing material; required only when enabling or replacing a webhook policy.
   */
  webhook_secret?: string;
  /**
   * Completed unsuccessful primary dispatch claims before switching this incident to fallback.
   */
  after_failures?: number;
};

