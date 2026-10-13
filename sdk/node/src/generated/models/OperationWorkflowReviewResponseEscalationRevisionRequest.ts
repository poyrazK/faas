/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit scoped review of the current completed coordinator action and creation of a revised commitment only while the handoff remains unaccepted and undeclined. Supply current episode timestamps and preserve parent owner, plan and exact check-in; omit parent reviews. Cannot combine with other response actions. Evidence unavailable is not clear or application success. Requires the recorded non-clear review timestamp and a fresh non-clear recheck; deadline must be future and within seven days.
 */
export type OperationWorkflowReviewResponseEscalationRevisionRequest = {
  decision_at: string;
  required_at: string;
  handed_off_at: string;
  escalated_at: string;
  acknowledged_at: string;
  completed_at: string;
  review_at: string;
  due_at: string;
  /**
   * Nonblank trimmed control-free UTF-8 coordinator label, at most 256 bytes.
   */
  coordinator: string;
  /**
   * Nonblank revised action plan, at most 1024 UTF-8 bytes; line breaks and tabs allowed.
   */
  plan: string;
};

