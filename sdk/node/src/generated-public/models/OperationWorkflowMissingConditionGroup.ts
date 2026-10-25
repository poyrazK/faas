/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * Missing reporting coverage for one current contract and state and pinned refresh owner. Unassigned means owner is absent. blocked_time counts each workflow once and measures full observed workflow blocked duration, not duration caused by the missing condition or owner. Groups overlap across owners and do not assign business severity.
 */
export type OperationWorkflowMissingConditionGroup = {
  contract_version: number;
  state: string;
  /**
   * Public refresh owner from the expected condition declaration in the latest reporting Operation pinned definition.
   */
  owner?: string;
  unassigned: boolean;
  missing_condition_count: number;
  blocked_time: OperationWorkflowDurationDistribution;
};

