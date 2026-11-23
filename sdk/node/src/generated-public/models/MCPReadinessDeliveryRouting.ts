/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Credential-free destination progress for one immutable readiness event. A recovery can remain pending after fallback succeeds while the previously informed primary is still outstanding.
 */
export type MCPReadinessDeliveryRouting = {
  /**
   * Native primary policy version pinned at first delivery; omitted for legacy webhook routing.
   */
  pagerduty_version?: number;
  /**
   * Fallback configuration version captured when this incident first claimed delivery.
   */
  policy_version: number;
  /**
   * Receiver role used for this event's most recent dispatch claim.
   */
  last_destination?: 'primary' | 'fallback';
  /**
   * Explanation for selecting a receiver after failover; omitted for ordinary primary delivery.
   */
  failover_reason?: 'primary_failure_threshold' | 'incident_sticky_fallback' | 'recovery_to_primary' | 'primary_queue_blocked';
  /**
   * Number of primary dispatch claims including expired leases.
   */
  primary_attempts: number;
  /**
   * Number of fallback dispatch claims for this logical event.
   */
  fallback_attempts: number;
  /**
   * Number of fenced unsuccessful primary completions for this event.
   */
  primary_failures: number;
  /**
   * Whether primary accepted this event in a completed delivery claim.
   */
  primary_delivered: boolean;
  /**
   * Whether fallback accepted this event in a completed delivery claim.
   */
  fallback_delivered: boolean;
};

