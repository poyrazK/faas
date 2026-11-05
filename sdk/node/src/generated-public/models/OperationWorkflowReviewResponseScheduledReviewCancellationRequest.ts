/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cancel the pending evidence check for the current completed coordinator action using exact episode, completion and due timestamps. Preserve parent owner, plan and exact check-in, even if overdue; omit parent reviews. Cannot combine with other response actions. Cancellation can win while an observation is in flight; a published or already canceled check rejects cancellation. No source observation is required.
 */
export type OperationWorkflowReviewResponseScheduledReviewCancellationRequest = {
  decision_at: string;
  required_at: string;
  handed_off_at: string;
  escalated_at: string;
  acknowledged_at: string;
  completed_at: string;
  due_at: string;
  /**
   * Nonblank UTF-8 cancellation reason, at most 1024 bytes; line breaks and tabs allowed.
   */
  reason: string;
};

