/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC-aligned hourly request analytics bucket.
 */
export type RequestAnalyticsTimeseriesPoint = {
  /**
   * Inclusive UTC start of the one-hour bucket.
   */
  start: string;
  requests: number;
  error_requests: number;
  error_rate_pct: number;
  cold_boots: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
};

