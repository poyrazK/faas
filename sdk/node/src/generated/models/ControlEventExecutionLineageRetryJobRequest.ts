/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original job creation guard and optional idempotency UUID for a pause or resume command.
 */
export type ControlEventExecutionLineageRetryJobRequest = {
  /**
   * Optional nonzero command UUID; reuse it with the original action and creation guard after a lost response.
   */
  command_id?: string;
  /**
   * Original nonzero creation timestamp from the reviewed job. Context: Control Event Execution Lineage Retry Job Request.
   */
  expected_created_at: string;
};

