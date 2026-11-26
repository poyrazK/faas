/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current revocable webhook binding with a masked secret and the relative signed-ingress path.
 */
export type MCPReadinessPagerDutyAcknowledgementPolicy = {
  /**
   * Whether this destination currently accepts signed provider unacknowledgements. False or absent preserves acknowledgement-only behavior; true releases only matching tracked provider ownership.
   */
  sync_unacknowledgements?: boolean;
  /**
   * Selected native PagerDuty destination
   */
  destination?: 'primary' | 'fallback';
  /**
   * Captured fallback configuration version; present for a fallback binding.
   */
  fallback_version?: number;
  version: number;
  enabled: boolean;
  /**
   * Relative signed callback URL for this binding; explicit destination reads always return a destination-specific URL. Existing original callback URLs remain bound to their original destination.
   */
  webhook_path: string;
  /**
   * Native primary receiver version captured when the acknowledgement binding was saved; omitted for fallback binding.
   */
  pagerduty_version?: number;
  /**
   * Regional PagerDuty receiver bound to this enabled webhook policy.
   */
  region?: 'us' | 'eu';
  /**
   * Exact provider service accepted by the enabled webhook binding.
   */
  service_id?: string;
  /**
   * Fixed mask for an enabled signing credential; no plaintext or ciphertext is returned.
   */
  signing_secret_masked?: '***';
};

