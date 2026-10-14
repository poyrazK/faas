/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Worker Batch Claim Request.
 */
export type RealtimeWorkerBatchClaimRequest = {
  /**
   * Optional; must match the credential identity if supplied.
   */
  worker?: string;
  limit: number;
  /**
   * Realtime worker batch claim request properties wait seconds. Wait for available work, checking every 500 ms without holding a transaction. Returns promptly on pause/drain or blockers. At most 64 waiting requests per apid. Cancellation stops waiting; uncertain claims must not be retried automatically.
   */
  wait_seconds?: number;
};

