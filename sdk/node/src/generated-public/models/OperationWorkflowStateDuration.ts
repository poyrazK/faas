/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed time and interval count for one state and contract version within a selected workflow instance.
 */
export type OperationWorkflowStateDuration = {
  contract_version: number;
  state: string;
  observed_seconds: number;
  observation_count: number;
  ongoing: boolean;
};

