/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowAttentionStats } from './OperationWorkflowAttentionStats.js';
/**
 * Attention statistics for one value of the selected grouping dimension. Owner grouping uses an empty value for unassigned blockers; owner counts and ages cover that owner only.
 */
export type OperationWorkflowAttentionGroup = {
  value: string;
  stats: OperationWorkflowAttentionStats;
};

