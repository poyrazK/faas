/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned thresholds captured with the decision; never inferred from current settings.
 */
export type RouteHealthEvaluationPolicy = {
  version: 1;
  windows: number;
  window_seconds: number;
  ingestion_lag_seconds: number;
  minimum_requests: number;
  minimum_errors: number;
  /**
   * Minimum candidate 5xx rate as a fraction.
   */
  error_rate_floor: number;
  /**
   * Minimum additional 5xx rate as a fraction.
   */
  error_rate_delta: number;
  error_rate_factor: number;
  minimum_latency_requests: number;
  latency_quantile: number;
  latency_factor: number;
  latency_delta_ms: number;
  /**
   * Floating point tolerance used in comparisons.
   */
  comparison_epsilon: number;
};

