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
   * One-shot evidence check time pinned at completion. Ordinary edits and manual evidence reviews do not postpone it.
   */
  review_due_at?: string;
  /**
   * Scheduled evidence check delivery marker committed atomically with its observation and durable inbox notification. Existing notification retention bounds apply.
   */
  scheduled_review_at?: string;
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

