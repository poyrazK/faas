/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialFailure } from './WorkflowInitialFailure.js';
import type { WorkflowRunRecoveryTarget } from './WorkflowRunRecoveryTarget.js';
export type WorkflowInitialRecoveryPreview = {
  failure: WorkflowInitialFailure;
  observed_at: string;
  /**
   * Stable diagnostic reason such as eligible, changed, run_not_retained, capacity or resume_ineligible.
   */
  reason: string;
  blockers: Array<string>;
  target?: WorkflowRunRecoveryTarget;
};

