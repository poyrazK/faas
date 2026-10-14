/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One normalized query pattern within a database dependency, with the same latency and failure comparison as its dependency.
 */
export type DebugDependencyQueryItem = {
  /**
   * Redacted, normalized statement; never contains literal values.
   */
  pattern: string;
  calls: number;
  error_calls: number;
  p95_ms: number;
  baseline_p95_ms?: number;
  current_p95_ms?: number;
  regression_factor?: number;
  regression: boolean;
  baseline_error_rate_pct?: number;
  current_error_rate_pct?: number;
  failure_regression: boolean;
  top_error_type?: string;
};

