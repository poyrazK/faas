/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
export type OperationWorkflowBlockerPerformance = {
  contract_version: number;
  operation: string;
  code: string;
  /**
   * Empty means unassigned.
   */
  owner: string;
  duration: OperationWorkflowDurationDistribution;
};

