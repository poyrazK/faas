/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseEscalationActionReview } from './OperationWorkflowReviewResponseEscalationActionReview.js';
import type { OperationWorkflowReviewResponseLink } from './OperationWorkflowReviewResponseLink.js';
/**
 * Immutable human completion of escalation coordination work, with linked review references and server actor and time. Existing overdue markers remain historical.
 */
export type OperationWorkflowReviewResponseEscalationCompletion = {
  /**
   * OperationWorkflowReviewResponseEscalationCompletion.review. Scoped evidence review for completed coordinator work, separate from business success.
   */
  review?: OperationWorkflowReviewResponseEscalationActionReview;
  acknowledged_at: string;
  summary: string;
  evidence: Array<OperationWorkflowReviewResponseLink>;
  at: string;
  actor: string;
};

