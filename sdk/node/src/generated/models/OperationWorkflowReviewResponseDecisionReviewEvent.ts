/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseDecision } from './OperationWorkflowReviewResponseDecision.js';
import type { OperationWorkflowReviewResponseDecisionReview } from './OperationWorkflowReviewResponseDecisionReview.js';
/**
 * One durable revalidation alert per explicit decision, routed to the current response owner without changing intent, deadlines or application execution.
 */
export type OperationWorkflowReviewResponseDecisionReviewEvent = {
  /**
   * OperationWorkflowReviewResponseDecisionReviewEvent.source_notification_id. Original risk alert identity.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReviewResponseDecisionReviewEvent.source_sequence. Original alert sequence in this inbox.
   */
  source_sequence: number;
  /**
   * OperationWorkflowReviewResponseDecisionReviewEvent.decision. Immutable prior human intent and reviewed condition.
   */
  decision: OperationWorkflowReviewResponseDecision;
  /**
   * OperationWorkflowReviewResponseDecisionReviewEvent.review. First changed condition and its differences.
   */
  review: OperationWorkflowReviewResponseDecisionReview;
};

