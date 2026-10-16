/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One named event for an operation (ADR-1197).
 */
export type OperationSignalRequest = {
  /**
   * Idempotency key for this delivery
   */
  signal_id: string;
  event: string;
  /**
   * JSON delivered as the wait step's output; at most 64 KiB.
   */
  payload?: any;
};

