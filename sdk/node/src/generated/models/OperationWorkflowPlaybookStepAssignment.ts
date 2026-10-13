/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookProgressItem } from './OperationWorkflowPlaybookProgressItem.js';
import type { OperationWorkflowPlaybookReviewImpact } from './OperationWorkflowPlaybookReviewImpact.js';
import type { OperationWorkflowPlaybookStepReadiness } from './OperationWorkflowPlaybookStepReadiness.js';
import type { OperationWorkflowReadinessAssignmentChange } from './OperationWorkflowReadinessAssignmentChange.js';
import type { OperationWorkflowReadinessEscalationResponse } from './OperationWorkflowReadinessEscalationResponse.js';
/**
 * OperationWorkflowPlaybookStepAssignment. Assigned step attention in the existing inbox and follow-up queue. Polling preserves manual coordination and bounded ownership history.
 */
export type OperationWorkflowPlaybookStepAssignment = {
  /**
   * OperationWorkflowPlaybookStepAssignment.review_impact. Pinned application-declared business result and priority protected by validity review; does not assert actual business failure or change reported outcome.
   */
  review_impact?: OperationWorkflowPlaybookReviewImpact;
  /**
   * OperationWorkflowPlaybookStepAssignment.dependency_review_status. Latest source checklist decision for this review episode, observed by an active watch; independent of inbox acknowledgment or manual completion.
   */
  dependency_review_status?: 'awaiting' | 'confirmed' | 'redo_requested' | 'superseded';
  /**
   * OperationWorkflowPlaybookStepAssignment.dependency_review_prerequisite. Reopened ancestor label captured from retained progress history when available; the immutable review revision identifies the event if history has been truncated.
   */
  dependency_review_prerequisite?: string;
  /**
   * OperationWorkflowPlaybookStepAssignment.dependency_review. Immutable prior completion evidence and prerequisite reopening revision for a human validity review task. Recovery does not close it; an explicit review or newer regression supersedes this episode.
   */
  dependency_review?: OperationWorkflowPlaybookProgressItem;
  /**
   * OperationWorkflowPlaybookStepAssignment.readiness. Latest accepted prerequisite observation; existing human queue coordination remains unchanged when prerequisites reopen.
   */
  readiness?: OperationWorkflowPlaybookStepReadiness;
  /**
   * OperationWorkflowPlaybookStepAssignment.source_revision. Last synchronized progress revision, used to retain intervening owner changes; incomplete source history prevents fallback.
   */
  source_revision?: number;
  response?: OperationWorkflowReadinessEscalationResponse;
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
   * OperationWorkflowPlaybookStepAssignment.manual_owner. Manual inbox changes or automatic fallback protect queue ownership from later checklist synchronization.
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

