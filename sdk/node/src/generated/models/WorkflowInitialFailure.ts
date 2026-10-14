/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One captured generation-zero workflow failure.
 */
export type WorkflowInitialFailure = {
  id: string;
  app_id: string;
  run_id: string;
  workflow: string;
  failure_status: 'failed' | 'dead';
  observed_at: string;
  completed_at?: string;
};

