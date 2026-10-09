/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
export type OperationWorkflowVerificationPerformance = {
  /**
   * Empty means unassigned.
   */
  owner: string;
  pending_resolution_count: number;
  duration: OperationWorkflowDurationDistribution;
};

