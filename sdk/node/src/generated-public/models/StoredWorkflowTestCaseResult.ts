/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowTestStepResult } from './StoredWorkflowTestStepResult.js';
/**
 * Safe overall case result and bounded per-step assertion summaries.
 */
export type StoredWorkflowTestCaseResult = {
  name: string;
  passed: boolean;
  issue?: 'invalid_expectations' | 'simulation_rejected' | 'definition_invalid' | 'simulation_incomplete' | 'assertion_failed';
  steps_truncated?: boolean;
  steps: Array<StoredWorkflowTestStepResult>;
};

