/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveExpectation } from './StoredWorkflowLiveExpectation.js';
/**
 * Explicit live assertions frozen when a saved case is admitted for execution.
 */
export type StoredWorkflowLiveTest = {
  /**
   * Desired terminal run state; omitted means succeeded. Cancellation always fails verification.
   */
  expected_run_status?: 'succeeded' | 'failed' | 'dead';
  expectations: Array<StoredWorkflowLiveExpectation>;
};

