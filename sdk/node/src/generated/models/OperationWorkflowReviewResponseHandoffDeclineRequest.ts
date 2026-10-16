/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Decline one current unaccepted handoff without a decision or acceptance. Retain its owner, plan and exact check-in, even if already overdue; deadlines do not restart.
 */
export type OperationWorkflowReviewResponseHandoffDeclineRequest = {
  /**
   * OperationWorkflowReviewResponseHandoffDeclineRequest.handed_off_at. Current handoff timestamp; required by acceptance and decline after manual reassignment. Legacy automatic handoffs can omit it.
   */
  handed_off_at?: string;
  /**
   * OperationWorkflowReviewResponseHandoffDeclineRequest.decision_at. Exact decision timestamp of the handed-off episode.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffDeclineRequest.required_at. Exact requirement timestamp; stale episodes conflict.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffDeclineRequest.reason. Nonblank UTF-8 reason up to 1024 bytes; line breaks and tabs allowed.
   */
  reason: string;
};

