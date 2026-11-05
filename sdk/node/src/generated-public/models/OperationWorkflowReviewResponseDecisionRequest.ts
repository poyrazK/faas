/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit advisory continue, revise or pause decision on an existing active response. Saving re-observes scoped sources and checks the reviewed health digest again under the notification lock.
 */
export type OperationWorkflowReviewResponseDecisionRequest = {
  /**
   * OperationWorkflowReviewResponseDecisionRequest.kind. Human intent; pause keeps check-ins active and does not stop application execution.
   */
  kind: 'continue' | 'revise' | 'pause';
  /**
   * OperationWorkflowReviewResponseDecisionRequest.reason. Nonblank UTF-8 explanation up to 1024 bytes; line breaks and tabs allowed.
   */
  reason: string;
  /**
   * OperationWorkflowReviewResponseDecisionRequest.health_fingerprint. Digest from the sampled health the operator reviewed; absent, legacy or changed observations conflict.
   */
  health_fingerprint: string;
};

