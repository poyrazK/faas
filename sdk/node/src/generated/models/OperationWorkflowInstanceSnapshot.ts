import type { OperationWorkflowReadinessOverview } from './OperationWorkflowReadinessOverview.js';
import type { OperationWorkflowDependencyTrace } from './OperationWorkflowDependencyTrace.js';
import type { OperationWorkflowDependencyImpact } from './OperationWorkflowDependencyImpact.js';
import type {OperationWorkflowRelatedInstance} from './OperationWorkflowRelatedInstance.js';
import type { OperationWorkflowDecision } from './OperationWorkflowDecision.js';
/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInstanceStep } from './OperationWorkflowInstanceStep.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
import type { OperationWorkflowStateHistoryEntry } from './OperationWorkflowStateHistoryEntry.js';
/**
 * Grouped view of the selected contract's declared steps and allowed transitions, current explicit state, and transition-history page for one workflow instance. Allowed transitions are contract edges by target Operation; the application still checks its business row and authorization before using one. Page-scoped facts follow the milestone cursor; retention-wide step summaries cover all matching facts still retained under the normal Operation retention rules.
 */
export type OperationWorkflowInstanceSnapshot = {
 readiness?: OperationWorkflowReadinessOverview;
 dependency_trace?: OperationWorkflowDependencyTrace;
 dependency_impact?: OperationWorkflowDependencyImpact;
 related_workflows?: OperationWorkflowRelatedInstance[];
  decision?: OperationWorkflowDecision;
  workflow: string;
  instance_id: string;
  contract_version: number;
  state?: OperationWorkflowState;
  steps: Array<OperationWorkflowInstanceStep>;
  allowed_transitions?: Array<OperationWorkflowInstanceTransition>;
  transitions: Array<OperationWorkflowStateHistoryEntry>;
  /**
   * True when either milestone or transition-history cursor has more pages.
   */
  has_more: boolean;
  next_milestone_cursor?: string;
  next_transition_cursor?: string;
};

