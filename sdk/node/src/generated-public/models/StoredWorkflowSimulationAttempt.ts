/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe summary of one supplied hypothetical attempt outcome.
 */
export type StoredWorkflowSimulationAttempt = {
  attempt: number;
  outcome: 'success' | 'failure' | 'timeout';
  http_status?: number;
};

