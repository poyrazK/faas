/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookStepReadiness } from './OperationWorkflowPlaybookStepReadiness.js';
/**
 * OperationWorkflowPlaybookDependencyState. One sampled human follow-up step observation, distinct from application reports and queue coordination.
 */
export type OperationWorkflowPlaybookDependencyState = {
  /**
   * OperationWorkflowPlaybookDependencyState.outcome_code. Exact pinned outcome list.
   */
  outcome_code: string;
  /**
   * OperationWorkflowPlaybookDependencyState.item_index. Local immutable step index.
   */
  item_index: number;
  /**
   * OperationWorkflowPlaybookDependencyState.label. Exact pinned step label.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookDependencyState.status. Observed human review status.
   */
  status: 'pending' | 'completed';
  /**
   * OperationWorkflowPlaybookDependencyState.owner. Current checklist step owner; inbox overrides remain separate.
   */
  owner: string;
  readiness: OperationWorkflowPlaybookStepReadiness;
};

