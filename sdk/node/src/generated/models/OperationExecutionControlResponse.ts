/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current HTTP attempt observation. Contains no input, result, owner or capability; grants no renewal.
 */
export type OperationExecutionControlResponse = {
  operation_id: string;
  invocation_id: string;
  attempt: number;
  cancellation_requested: boolean;
  deadline_at: string;
  /**
   * Current claim expiry, capped by deadline_at. Only the scheduler can renew it.
   */
  lease_expires_at: string;
  /**
   * Server observation time. Derive a conservative local duration and subtract request latency.
   */
  observed_at: string;
  /**
   * Recommended polling interval; clients also stop at the earlier lease or deadline bound.
   */
  poll_after_ms: number;
};

