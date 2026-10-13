/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventExecutionLineageRetrySelection = {
  outbox_id: number;
  expected_revision: number;
  /**
   * Exact original acceptance timestamp from the reviewed case.
   */
  accepted_at: string;
};

