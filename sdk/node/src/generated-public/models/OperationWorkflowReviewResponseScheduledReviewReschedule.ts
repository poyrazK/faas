/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable schedule change audit with previous and replacement due times, completion identity, human reason and server actor and time. Retained change history records each reschedule; current completion keeps the latest change and original due time.
 */
export type OperationWorkflowReviewResponseScheduledReviewReschedule = {
  completed_at: string;
  previous_due_at: string;
  due_at: string;
  reason: string;
  actor: string;
  at: string;
};

