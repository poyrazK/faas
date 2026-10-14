/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One reviewed lineage case with its revision and optional diagnostic incarnation guard.
 */
export type EventExecutionLineageRetrySelection = {
  /**
   * Required when the reviewed case has diagnostic_id; omit for legacy cases.
   */
  expected_diagnostic_id?: string;
  outbox_id: number;
  expected_revision: number;
  /**
   * Exact original acceptance timestamp from the reviewed case.
   */
  accepted_at: string;
};

