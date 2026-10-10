/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Represented request and server-error counts, error rate, and optional weighted p95 for one deployment in one window.
 */
export type RouteHealthCounts = {
  requests: number;
  server_errors: number;
  error_rate: number;
  /**
   * Weighted p95 estimate in milliseconds from collapsed telemetry representatives. Omitted when latency is not selected or evidence is unavailable; zero is a valid observation.
   */
  p95_latency_ms?: number;
  /**
   * Synthetic probe responses rejected by customer auth gates (401/403). Only present in synthetic_windows; a window where at least half of either side was rejected is unknown (probe_unauthenticated).
   */
  unauthenticated?: number;
};

