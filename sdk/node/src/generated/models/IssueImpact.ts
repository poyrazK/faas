/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed retained customer impact within an explicit time window; unknown identity remains unattributed.
 */
export type IssueImpact = {
  window_start: string;
  window_end: string;
  identified_customers: number;
  observed_events: number;
  unattributed_events: number;
  /**
   * Legacy HTTP batches in this impact window have unknown folded weights; observed events are a lower bound.
   */
  unknown_http_batches?: number;
  coverage: string;
};

