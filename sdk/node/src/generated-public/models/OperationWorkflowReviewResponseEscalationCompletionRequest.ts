/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete one current escalation coordinator commitment with an action summary and saved review evidence. Preserve parent owner, plan and exact check-in, even if overdue; omit parent reviews. Cannot combine with acknowledgment, decision, acceptance, decline or reassignment. Accepted or declined handoffs can still record completed work for the same retained episode, but replaced or resolved episodes cannot. Does not establish application success.
 */
export type OperationWorkflowReviewResponseEscalationCompletionRequest = {
  /**
   * Optional one-shot evidence check delay from server completion time, 60 seconds to seven days. Omission leaves the completion unscheduled.
   */
  review_after_seconds?: number;
  decision_at: string;
  required_at: string;
  handed_off_at: string;
  escalated_at: string;
  acknowledged_at: string;
  /**
   * Nonblank UTF-8 summary of performed coordination work, limited to 1024 bytes; line breaks and tabs are allowed.
   */
  summary: string;
  /**
   * Zero-based indices into the current parent response reviews. References are copied into immutable evidence; they are not fresh application verification or a claim that blockers cleared.
   */
  review_indices: Array<number>;
};

