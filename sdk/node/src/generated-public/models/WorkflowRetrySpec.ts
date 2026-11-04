/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retry policy for one workflow step.
 */
export type WorkflowRetrySpec = {
  max_attempts: number;
  backoff?: 'fixed' | 'exponential';
};

