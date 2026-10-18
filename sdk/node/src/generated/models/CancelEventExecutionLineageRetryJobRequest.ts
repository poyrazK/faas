/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original job creation guard and optional command identity for cancelling bulk lineage retries.
 */
export type CancelEventExecutionLineageRetryJobRequest = {
  /**
   * Optional nonzero command UUID; reuse with the original cancel action and creation guard after a lost response.
   */
  command_id?: string;
  /**
   * Exact original created_at from the reviewed retained job; fences a replacement after expiry.
   */
  expected_created_at: string;
};

