/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RetryEventExecutionLineageCaseRequest = {
  /**
   * Required when the reviewed case has diagnostic_id; omit for legacy cases.
   */
  expected_diagnostic_id?: string;
  request_id: string;
  expected_revision: number;
};

