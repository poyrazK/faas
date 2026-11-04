/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reassign a currently declined handoff to a different parent response owner. Preserve plan, check-in and review deadline; start a new acceptance clock. Cannot combine with a decision, acceptance or decline.
 */
export type OperationWorkflowReviewResponseHandoffReassignmentRequest = {
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentRequest.decision_at. Exact reviewed decision timestamp.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentRequest.required_at. Exact review requirement timestamp.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentRequest.declined_at. Exact decline timestamp to route; stale decline conflicts.
   */
  declined_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentRequest.reason. Nonblank UTF-8 explanation up to 1024 bytes; line breaks and tabs allowed.
   */
  reason: string;
};

