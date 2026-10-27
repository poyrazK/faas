/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Claim Request.
 */
export type RealtimeConsumerGroupClaimRequest = {
  worker: string;
  /**
   * Realtime consumer group claim request properties wait seconds. Wait for available work, checking every 500 ms without holding a transaction. Returns promptly on pause/drain or blockers. At most 64 waiting requests per the API. Cancellation stops waiting; uncertain claims must not be retried automatically.
   */
  wait_seconds?: number;
};

