/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Version-fenced replacement of a PagerDuty acknowledgement webhook binding and write-only signing secret.
 */
export type SetMCPReadinessPagerDutyAcknowledgementsRequest = {
  /**
   * Opt in to signed incident.unacknowledged events for this destination. Only tracked provider acknowledgements can be released; other destination and manual acknowledgements remain protected.
   */
  sync_unacknowledgements?: boolean;
  /**
   * Native PagerDuty receiver to bind or disable. Enabling defaults to primary; disabling without a destination selects the original callback binding.
   */
  destination?: 'primary' | 'fallback';
  /**
   * Required for fallback binding instead of expected_pagerduty_version; must match an enabled native PagerDuty fallback.
   */
  expected_fallback_version?: number;
  /**
   * Current selected destination acknowledgement policy version; zero denotes unconfigured.
   */
  expected_version: number;
  /**
   * Enable signed inbound acknowledgements or immediately revoke the current signing secret.
   */
  enabled: boolean;
  /**
   * Current enabled native primary PagerDuty version; required for primary binding. Omit for fallback or disabled policies.
   */
  expected_pagerduty_version?: number;
  /**
   * PagerDuty service selected by the webhook subscription filter; required only when enabling.
   */
  service_id?: string;
  /**
   * Exact ASCII v3 webhook subscription signing secret from PagerDuty; required when enabling and omitted when disabling.
   */
  signing_secret?: string;
};

