/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialFailure } from './WorkflowInitialFailure.js';
/**
 * Page of captured initial workflow failures.
 */
export type WorkflowInitialFailures = {
  observed_at: string;
  coverage: 'partial';
  items: Array<WorkflowInitialFailure>;
  next_after?: string;
};

