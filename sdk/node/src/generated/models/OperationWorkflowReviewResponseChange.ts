/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowReviewResponseChange. Authenticated response update snapshot.
 */
export type OperationWorkflowReviewResponseChange = {
  /**
   * OperationWorkflowReviewResponseChange.fallback_owner. Fallback policy captured for this response change.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponseChange.reason. Explicit human edit or periodic missed-check-in event; absent in older history.
   */
  reason?: 'human_update' | 'check_in_overdue';
  /**
   * OperationWorkflowReviewResponseChange.owner. Responsible team label; trimmed control-free UTF-8 limited to 256 bytes.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseChange.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line breaks and tabs are allowed.
   */
  plan: string;
  /**
   * OperationWorkflowReviewResponseChange.status. Active ownership or human resolution after a fresh scoped check of linked source reviews.
   */
  status: 'active' | 'resolved';
  /**
   * OperationWorkflowReviewResponseChange.check_in_at. Required future check-in for active responses; absent when resolved. Replaces inbox attention time without changing the original alert response deadline.
   */
  check_in_at?: string;
  /**
   * OperationWorkflowReviewResponseChange.at. Server change time.
   */
  at: string;
  /**
   * OperationWorkflowReviewResponseChange.actor. Authenticated response editor.
   */
  actor: string;
};

