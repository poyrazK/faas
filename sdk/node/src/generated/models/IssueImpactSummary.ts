/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed retained occurrences, verified distinct customers, and unattributed events from the previous 24 hours.
 */
export type IssueImpactSummary = {
  identified_customers: number;
  observed_events: number;
  unattributed_events: number;
  /**
   * Legacy HTTP records with unknown folded weights; observed event counts are lower bounds.
   */
  unknown_http_batches?: number;
};

