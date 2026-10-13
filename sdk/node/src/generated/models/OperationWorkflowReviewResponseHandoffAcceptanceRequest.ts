/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Accept the current reassigned review for its responsible team. Parent response must remain active with the handed-off owner and a future check-in, without an accompanying decision.
 */
export type OperationWorkflowReviewResponseHandoffAcceptanceRequest = {
  /**
   * OperationWorkflowReviewResponseHandoffAcceptanceRequest.handed_off_at. Current handoff timestamp; required by acceptance and decline after manual reassignment. Legacy automatic handoffs can omit it.
   */
  handed_off_at?: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptanceRequest.decision_at. Exact decision timestamp of the handed-off episode.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptanceRequest.required_at. Exact requirement timestamp of the handed-off episode; stale episodes conflict.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptanceRequest.note. Nonblank UTF-8 acceptance note up to 1024 bytes; line breaks and tabs allowed.
   */
  note: string;
};

