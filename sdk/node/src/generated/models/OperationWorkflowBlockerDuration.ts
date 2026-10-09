/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type OperationWorkflowBlockerDuration = {
  contract_version: number;
  operation: string;
  code: string;
  /**
   * Empty means unassigned.
   */
  owner: string;
  observed_seconds: number;
  observation_count: number;
  ongoing: boolean;
};

