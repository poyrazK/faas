/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationTestStepResult } from './AutomationTestStepResult.js';
/**
 * Payload-free verdict for the frozen live assertions at one terminal run state.
 */
export type AutomationLiveReport = {
  checked_at: string;
  passed: boolean;
  issue?: string;
  run_status: string;
  resume_count: number;
  run_updated_at: string;
  steps: Array<AutomationTestStepResult>;
};

