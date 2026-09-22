/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregated mirror drift counts over a trailing window. PR-A2
 * returns zeros (PR-A1's ledger has no writers until A3 ships
 * the runtime dispatch); post-A3 this is the dashboard widget's
 * data source.
 *
 */
export type MirrorSummaryResponse = {
  total_invocations: number;
  /**
   * Requests with any status, schema, or body difference; each request is counted once.
   */
  changed_response_count: number;
  /**
   * 100 × changed_response_count / total_invocations; zero when the window is empty.
   */
  changed_response_percent: number;
  status_diff_count: number;
  schema_diff_count: number;
  body_diff_count: number;
  /**
   * Signed: mirror - source. Positive = mirror slower.
   */
  mean_latency_diff_ms: number;
  p99_latency_diff_ms: number;
  crash_count: number;
  /**
   * The window's length in seconds. Matches the requested `?window=` value.
   */
  window_seconds: number;
};

