/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Idempotent reevaluation identity and reviewed revision guard for a single lineage case.
 */
export type RetryEventExecutionLineageCaseRequest = {
  /**
   * Required when the reviewed case has diagnostic_id; omit for legacy cases. Context: Retry Event Execution Lineage Case Request.
   */
  expected_diagnostic_id?: string;
  request_id: string;
  expected_revision: number;
};

