/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reschedule a pending evidence check using exact current episode, completion and due timestamps. New time must differ from current due time and be future within seven days. Preserve parent owner, plan and exact check-in, even if overdue; omit parent reviews. Cannot combine with other response actions. Published or canceled checks reject rescheduling; no source observation is required.
 */
export type OperationWorkflowReviewResponseScheduledReviewRescheduleRequest = {
  decision_at: string;
  required_at: string;
  handed_off_at: string;
  escalated_at: string;
  acknowledged_at: string;
  completed_at: string;
  due_at: string;
  new_due_at: string;
  /**
   * Nonblank UTF-8 explanation for the schedule change, at most 1024 bytes; line breaks and tabs allowed.
   */
  reason: string;
};

