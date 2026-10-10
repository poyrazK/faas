/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts of every saved retry request in the inspected app job page before filtering. Request statuses partition request_count and incomplete evidence overlaps those statuses.
 */
export type EventRecoveryNotificationRetryBacklogTotals = {
  request_count: number;
  succeeded_count: number;
  failed_count: number;
  pending_count: number;
  inconclusive_count: number;
  incomplete_evidence_count: number;
};

