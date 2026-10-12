/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Retry Policy Request.
 */
export type RealtimeConsumerGroupRetryPolicyRequest = {
  enabled: boolean;
  base_delay_seconds: number;
  /**
   * Must be at least base_delay_seconds.
   */
  max_delay_seconds: number;
  jitter: boolean;
  /**
   * Exact case-sensitive codes; unspecified codes remain retryable.
   */
  permanent_failure_codes?: Array<string>;
};

