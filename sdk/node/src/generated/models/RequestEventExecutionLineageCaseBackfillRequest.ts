/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Idempotent request identity and previous-scan guard for a bounded diagnostic backfill.
 */
export type RequestEventExecutionLineageCaseBackfillRequest = {
  request_id: string;
  /**
   * Omit for the first request; otherwise use the latest completed, cancelled or expired request UUID. Repeated submissions preserve their original guard.
   */
  expected_previous_request_id?: string;
};

