/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed event-window counts and matched retry timing, without causal or live-state inference.
 */
export type WorkflowRecoveryReportTotals = {
  attempts: number;
  succeeded: number;
  failed: number;
  cancelled: number;
  other_results: number;
  /**
   * Attempts without a result observed in this window; not a live unresolved-child count.
   */
  without_observed_result: number;
  pauses: number;
  timed_results: number;
  /**
   * Mean application-to-recorded-result time for matched child IDs with ordered timestamps; absent without timing samples.
   */
  mean_retry_result_seconds?: number;
};

