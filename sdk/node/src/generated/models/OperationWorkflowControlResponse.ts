/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current native workflow attempt observation. Contains no input, result, owner or capability; grants no renewal.
 */
export type OperationWorkflowControlResponse = {
  operation_id: string;
  workflow_run_id: string;
  workflow_step: string;
  generation: number;
  attempt: number;
  cancellation_requested: boolean;
  deadline_at: string;
  /**
   * Native run lease expiry, bounded by this attempt's fixed deadline. Only native scheduler ownership can extend the lease.
   */
  lease_expires_at: string;
  /**
   * Native authority observation time; use relative durations and subtract the time spent fetching this observation.
   */
  observed_at: string;
  /**
   * Suggested delay before observing this native attempt again. The local time budget expires independently.
   */
  poll_after_ms: number;
};

