/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable decline for a responsible team recorded by an authenticated authorized operator, without proof of team membership. Retained history carries its one-time notification intent.
 */
export type OperationWorkflowReviewResponseHandoffDecline = {
  /**
   * OperationWorkflowReviewResponseHandoffDecline.decision_at. Reviewed decision timestamp.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.required_at. Original review requirement timestamp.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.at. Server decline time.
   */
  at: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.notified_at. One-time notification publication marker; bounded history or inbox retention can limit delivery.
   */
  notified_at?: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.owner. Declining responsible team.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.previous_owner. Previous owner to notify for manual routing.
   */
  previous_owner: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.actor. Authenticated operator recording decline.
   */
  actor: string;
  /**
   * OperationWorkflowReviewResponseHandoffDecline.reason. Operator reason; immutable after recording.
   */
  reason: string;
};

