/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseHealth } from './OperationWorkflowReviewResponseHealth.js';
/**
 * First changed observation for one explicit response decision. Remains latched until a new explicit decision; ordinary edits, acknowledgment and subsequent recovery do not clear it.
 */
export type OperationWorkflowReviewResponseDecisionReview = {
  /**
   * OperationWorkflowReviewResponseDecisionReview.decision_at. Time of the decision that needs revalidation.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.required_at. First sampled condition change, committed with its one-time inbox alert.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.changes. Differences from the immutable reviewed snapshot, including unavailable sources without claiming business failure.
   */
  changes: Array<string>;
  /**
   * OperationWorkflowReviewResponseDecisionReview.health. Frozen first changed health observation; live response health may continue changing.
   */
  health: OperationWorkflowReviewResponseHealth;
};

