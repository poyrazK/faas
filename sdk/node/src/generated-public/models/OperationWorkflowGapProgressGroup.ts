/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * Current gap group ordered by contract, state, owner and category, before condition previews. Each workflow contributes once per group. Blocked duration includes observed report intervals across permitted contract boundaries and does not measure gap age or business severity.
 */
export type OperationWorkflowGapProgressGroup = {
  contract_version: number;
  state: string;
  owner?: string;
  unassigned: boolean;
  gap_status: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed';
  missing_condition_count: number;
  blocked_time: OperationWorkflowDurationDistribution;
};

