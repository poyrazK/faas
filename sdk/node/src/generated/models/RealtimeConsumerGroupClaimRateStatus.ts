/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Claim Rate Status.
 */
export type RealtimeConsumerGroupClaimRateStatus = {
  rate_per_second: number;
  burst: number;
  /**
   * Cumulative committed batch admission decisions stopped by rate, including partial batches. Counts polling attempts, not withheld messages.
   */
  throttled_claim_requests: string;
  /**
   * Current earliest rate allowance delay; zero when available or disabled.
   */
  retry_after_milliseconds: number;
};

