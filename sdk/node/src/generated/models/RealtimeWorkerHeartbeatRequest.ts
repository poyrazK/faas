/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Worker Heartbeat Request.
 */
export type RealtimeWorkerHeartbeatRequest = {
  /**
   * Required on account route; optional for scoped workers and must match credential name if supplied. Sharing a credential aggregates processes.
   */
  worker?: string;
  ready: boolean;
  /**
   * Short diagnostic code only; never include secrets.
   */
  status_code?: string;
};

