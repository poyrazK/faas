/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationMilestone } from './OperationMilestone.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
import type { OperationWorkflowStateHistoryEntry } from './OperationWorkflowStateHistoryEntry.js';
/**
 * Page of retained public milestone facts in the selected ownership and business-reference boundary.
 */
export type OperationMilestonesResponse = {
  milestones: Array<OperationMilestone>;
  /**
   * Latest explicitly reported states for recent workflow instances under this business reference. With stale_only=true, this list contains only states past their app-declared age threshold. Milestone facts and state history are unaffected. This list is empty on Operation-specific feeds.
   */
  workflow_states?: Array<OperationWorkflowState>;
  /**
   * Retained app-reported changes for the exact workflow run when paired workflow selectors are supplied, ordered by app-assigned revision.
   */
  workflow_state_history?: Array<OperationWorkflowStateHistoryEntry>;
  /**
   * Opaque continuation bound to the same account
   */
  next_cursor?: string;
  /**
   * Independent continuation for workflow_state_history, bound to the same run and ownership filters.
   */
  next_workflow_state_cursor?: string;
};

