/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * Cohort verification-wait distribution and pending obligations for one verification owner.
 */
export type OperationWorkflowVerificationPerformance = {
  /**
   * Verification recipient for this cohort group; empty means unassigned.
   */
  owner: string;
  pending_resolution_count: number;
  duration: OperationWorkflowDurationDistribution;
};

