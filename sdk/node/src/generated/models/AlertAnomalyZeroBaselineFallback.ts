/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Absolute error-rate and failure-count guard for a zero historical baseline.
 */
export type AlertAnomalyZeroBaselineFallback = {
  /**
   * Minimum absolute error rate expressed as a percent.
   */
  minimum_error_rate_pct: number;
  /**
   * Minimum failed requests in the evaluation window.
   */
  minimum_failed_requests: number;
};

