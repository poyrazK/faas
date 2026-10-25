/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable cancellation audit for one pending scheduled evidence check. Retains original completion and due timestamps, server actor and time, and human reason. Does not change completion evidence, ownership or review obligations.
 */
export type OperationWorkflowReviewResponseScheduledReviewCancellation = {
  completed_at: string;
  due_at: string;
  reason: string;
  actor: string;
  at: string;
};

