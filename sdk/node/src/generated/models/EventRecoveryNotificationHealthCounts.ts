/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-phase notification job counts. Incomplete evidence or candidate truncation makes these lower bounds; partial observations cannot clear alerts and can only trigger satisfied greater-than comparisons.
 */
export type EventRecoveryNotificationHealthCounts = {
  counts_complete: boolean;
  overdue_jobs: number;
  dead_jobs: number;
  unknown_jobs: number;
  no_receivers_jobs: number;
};

