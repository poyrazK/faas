/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregated mirror drift counts over a trailing window. The changed
 * response percentage uses only complete comparisons; `incomplete_comparison_count`
 * separately reports missing or truncated response snapshots and scheduler
 * admission failures. Admission counters explain scheduler failures without
 * including them in `crash_count`.
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
  /**
   * Mirror invocations that returned 5xx or no response after admission; scheduler admission failures are counted separately.
   */
  crash_count: number;
  /**
   * Comparisons without a complete source and mirror response, including scheduler admission failures.
   */
  incomplete_comparison_count: number;
  /**
   * Mirror admissions that timed out before the gateway received an admitted target. A late schedd admission is cleaned up by schedd.
   */
  scheduler_admission_timeout_count: number;
  /**
   * Mirror admissions refused by a capacity or concurrency limit. These are incomplete comparisons, not guest crashes.
   */
  scheduler_admission_rejected_count: number;
  /**
   * Mirror admissions that failed for another scheduler error before returning a target.
   */
  scheduler_admission_error_count: number;
  /**
   * The window's length in seconds. Matches the requested `?window=` value.
   */
  window_seconds: number;
};

