/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type OperationWorkflowStateReportResponse = {
  id: string;
  operation_id: string;
  workflow: string;
  instance_id: string;
  /**
   * Previous app state when a declared transition was reported.
   */
  from_state?: string;
  state: string;
  revision: number;
};

