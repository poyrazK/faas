/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookReviewImpact } from './OperationWorkflowPlaybookReviewImpact.js';
/**
 * OperationWorkflowPlaybookStepAssignmentPolicy. Pinned team and completion window for one declared outcome follow-up step; coordination only.
 */
export type OperationWorkflowPlaybookStepAssignmentPolicy = {
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.review_impact. Pinned application-declared business result and priority protected by validity review; does not assert actual business failure or change reported outcome.
   */
  review_impact?: OperationWorkflowPlaybookReviewImpact;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.review_after_seconds. Optional validity review window starting at prerequisite reopening; zero omits the target. Pinned independently of the original step completion deadline. Inbox acknowledgment does not satisfy this target.
   */
  review_after_seconds?: number;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.review_fallback_owner. Optional fallback team after an unresolved review deadline; requires a positive review window and a team distinct from owner. Manual assignments prevent automatic takeover.
   */
  review_fallback_owner?: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.acknowledge_after_seconds. Optional response window from queue delivery; zero disables. Separate from the original step completion deadline.
   */
  acknowledge_after_seconds?: number;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.fallback_owner. Optional distinct fallback team; requires a positive response window. Assignment changes preserve human ownership history.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.label. Exact unique declared follow-up label.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.owner. Required trimmed control-free UTF-8 team label.
   */
  owner: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.complete_after_seconds. Completion window from first list review for independent steps, or first prerequisite readiness for dependent steps; bounded to seven days.
   */
  complete_after_seconds: number;
};

