/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit scoped review of the current completed coordinator action. Supply current episode timestamps and preserve parent owner, plan and exact check-in; omit parent reviews. Cannot combine with other response actions. Evidence unavailable is not clear or application success.
 */
export type OperationWorkflowReviewResponseEscalationActionReviewRequest = {
  decision_at: string;
  required_at: string;
  handed_off_at: string;
  escalated_at: string;
  acknowledged_at: string;
  completed_at: string;
};

