/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessOutcomeEscalationReference } from './OperationWorkflowReadinessOutcomeEscalationReference.js';
/**
 * OperationWorkflowPlaybookStepAssignmentReference. One retained queue delivery identity per outcome step and watch.
 */
export type OperationWorkflowPlaybookStepAssignmentReference = {
  /**
   * OperationWorkflowPlaybookStepAssignmentReference.dependency_review_revision. Revision of the delivered dependency review obligation; absent for ordinary assignments.
   */
  dependency_review_revision?: number;
  /**
   * OperationWorkflowPlaybookStepAssignmentReference.outcome_code. Pinned outcome selector.
   */
  outcome_code: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentReference.item_index. Exact local step index.
   */
  item_index: number;
  notification: OperationWorkflowReadinessOutcomeEscalationReference;
};

