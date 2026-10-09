/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compact immutable retry request count and decision time; use the detail endpoint for original per receiver outcomes and current delivery state.
 */
export type EventRecoveryNotificationRetryDecisionSummary = {
  request_id: string;
  decided_at: string;
  target_count: number;
  queued_count: number;
  skipped_count: number;
};

