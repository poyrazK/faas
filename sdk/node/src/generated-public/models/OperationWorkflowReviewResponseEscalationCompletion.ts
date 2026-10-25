/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseEscalationActionReview } from './OperationWorkflowReviewResponseEscalationActionReview.js';
import type { OperationWorkflowReviewResponseLink } from './OperationWorkflowReviewResponseLink.js';
import type { OperationWorkflowReviewResponseScheduledReviewCancellation } from './OperationWorkflowReviewResponseScheduledReviewCancellation.js';
import type { OperationWorkflowReviewResponseScheduledReviewReschedule } from './OperationWorkflowReviewResponseScheduledReviewReschedule.js';
/**
 * Immutable human completion of escalation coordination work, with linked review references and server actor and time. Existing overdue markers remain historical.
 */
export type OperationWorkflowReviewResponseEscalationCompletion = {
  /**
   * Original schedule time retained across explicit reschedules, even if older change history is pruned. Legacy completions initialize this from their current due time on first reschedule.
   */
  original_review_due_at?: string;
  /**
   * OperationWorkflowReviewResponseEscalationCompletion.review_reschedule. Explicit audited change of a pending evidence check time, preserving completed work and review obligations.
   */
  review_reschedule?: OperationWorkflowReviewResponseScheduledReviewReschedule;
  /**
   * OperationWorkflowReviewResponseEscalationCompletion.review_cancellation. Explicit cancellation of a pending scheduled evidence check with immutable actor, time and reason; manual reviews remain available.
   */
  review_cancellation?: OperationWorkflowReviewResponseScheduledReviewCancellation;
  /**
   * Current one-shot evidence check time. Only explicit rescheduling changes it; ordinary edits and manual evidence reviews do not.
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

