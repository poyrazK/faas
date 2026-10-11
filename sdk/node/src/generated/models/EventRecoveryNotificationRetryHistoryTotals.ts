/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts of all retained retry requests in the owned job before status filtering, observed at the enclosing history timestamp. Request status counts sum to request_count; evidence gaps are counted separately.
 */
export type EventRecoveryNotificationRetryHistoryTotals = {
  request_count: number;
  succeeded_count: number;
  failed_count: number;
  pending_count: number;
  inconclusive_count: number;
  incomplete_evidence_count: number;
};

