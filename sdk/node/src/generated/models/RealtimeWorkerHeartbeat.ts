/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Worker Heartbeat.
 */
export type RealtimeWorkerHeartbeat = {
  id: string;
  worker: string;
  ready: boolean;
  status_code: string;
  last_seen_at: string;
  /**
   * Persisted loss decision; sampled and may lag computed liveness.
   */
  lost: boolean;
  /**
   * Fresh heartbeat and currently eligible credential. Not proof of connectivity or ability to process work.
   */
  live: boolean;
};

