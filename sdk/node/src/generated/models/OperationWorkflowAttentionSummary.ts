/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowAttentionGroup } from './OperationWorkflowAttentionGroup.js';
import type { OperationWorkflowAttentionStats } from './OperationWorkflowAttentionStats.js';
/**
 * Attention totals across all matches and a paginated collection of grouped statistics.
 */
export type OperationWorkflowAttentionSummary = {
  group_by: 'workflow' | 'blocker_code' | 'target_operation' | 'customer' | 'dependency_status' | 'required_outcome_code';
  evaluated_at: string;
  totals: OperationWorkflowAttentionStats;
  groups: Array<OperationWorkflowAttentionGroup>;
  next_cursor?: string;
};

