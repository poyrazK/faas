/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit guarded command for per-subscription token-bucket pacing. Disabled retains configuration without enforcing the bucket.
 */
export type EventSubscriptionRateRequest = {
  /**
   * Nonzero command UUID; retained for 30 days, at most 128 per subscription.
   */
  command_id: string;
  /**
   * Current bucket revision, initially zero. Mismatch records a rejected receipt.
   */
  expected_revision: number;
  enabled: boolean;
  interval_millis: number;
  burst: number;
};

