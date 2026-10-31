/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * Eligible ongoing workflows with overdue condition reports assigned to one pinned refresh owner. Missing owners are unassigned. Each workflow counts once per owner even with multiple overdue conditions, and can appear under several owners. Durations cover the entire observed workflow, not time under the owner. Guidance never authorizes verification or execution.
 */
export type OperationWorkflowConditionRefreshOwnerGroup = {
  owner?: string;
  unassigned: boolean;
  overdue_condition_count: number;
  elapsed_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
};

