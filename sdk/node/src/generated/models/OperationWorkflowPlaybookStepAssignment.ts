/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessAssignmentChange } from './OperationWorkflowReadinessAssignmentChange.js';
/**
 * OperationWorkflowPlaybookStepAssignment. Assigned step attention in the existing inbox and follow-up queue. Polling preserves manual coordination and bounded ownership history.
 */
export type OperationWorkflowPlaybookStepAssignment = {
  /**
   * OperationWorkflowPlaybookStepAssignment.progress_id. Exact pinned checklist identity.
   */
  progress_id: string;
  /**
   * OperationWorkflowPlaybookStepAssignment.outcome_code. Exact outcome follow-up selector.
   */
  outcome_code: string;
  /**
   * OperationWorkflowPlaybookStepAssignment.item_index. Local step index.
   */
  item_index: number;
  /**
   * OperationWorkflowPlaybookStepAssignment.label. Pinned human follow-up label.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookStepAssignment.status. Latest accepted human step review; independent of inbox completion.
   */
  status: 'pending' | 'completed';
  /**
   * OperationWorkflowPlaybookStepAssignment.due_at. Original immutable completion deadline.
   */
  due_at: string;
  /**
   * OperationWorkflowPlaybookStepAssignment.last_step_owner. Latest synchronized checklist assignment, distinct from a manual queue override.
   */
  last_step_owner: string;
  /**
   * OperationWorkflowPlaybookStepAssignment.manual_owner. Any manual inbox ownership change prevents later checklist synchronization from overwriting it.
   */
  manual_owner: boolean;
  /**
   * OperationWorkflowPlaybookStepAssignment.history. Bounded retained checklist and inbox assignment changes.
   */
  history: Array<OperationWorkflowReadinessAssignmentChange>;
  /**
   * OperationWorkflowPlaybookStepAssignment.history_truncated. Older ownership changes are no longer retained.
   */
  history_truncated: boolean;
};

