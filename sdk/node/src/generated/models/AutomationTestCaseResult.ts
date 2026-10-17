/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationTestStepResult } from './AutomationTestStepResult.js';
/**
 * Safe overall case result and bounded per-step assertion summaries.
 */
export type AutomationTestCaseResult = {
  name: string;
  passed: boolean;
  issue?: 'invalid_expectations' | 'simulation_rejected' | 'definition_invalid' | 'simulation_incomplete' | 'assertion_failed';
  steps_truncated?: boolean;
  steps: Array<AutomationTestStepResult>;
};

