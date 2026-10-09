/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * Cohort duration distribution for one blocker target, code, contract version and observed owner.
 */
export type OperationWorkflowBlockerPerformance = {
  contract_version: number;
  operation: string;
  code: string;
  /**
   * Application-reported blocker owner for this cohort group; empty means unassigned.
   */
  owner: string;
  duration: OperationWorkflowDurationDistribution;
};

