/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregated mirror drift counts over a trailing window. The changed
 * response percentage uses only complete comparisons; `incomplete_comparison_count`
 * separately reports missing or truncated response snapshots.
 *
 */
export type MirrorSummaryResponse = {
  total_invocations: number;
  /**
   * Complete comparisons with any status, schema, or body difference; each request is counted once.
   */
  changed_response_count: number;
  /**
   * 100 × changed_response_count / complete comparisons; zero when none are complete.
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
   * Comparisons skipped because a response was missing or exceeded the capture limit.
   */
  incomplete_comparison_count: number;
  /**
   * The window's length in seconds. Matches the requested `?window=` value.
   */
  window_seconds: number;
};

