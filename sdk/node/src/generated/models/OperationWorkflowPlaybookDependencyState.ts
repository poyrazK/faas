/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookStepReadiness } from './OperationWorkflowPlaybookStepReadiness.js';
import type { OperationWorkflowReadinessOutcomeEscalationReference } from './OperationWorkflowReadinessOutcomeEscalationReference.js';
/**
 * OperationWorkflowPlaybookDependencyState. One sampled human follow-up step observation, distinct from application reports and queue coordination.
 */
export type OperationWorkflowPlaybookDependencyState = {
  /**
   * OperationWorkflowPlaybookDependencyState.regression. Latest delivered regression affecting this waiting step; cleared atomically after observed recovery.
   */
  regression?: OperationWorkflowReadinessOutcomeEscalationReference;
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

