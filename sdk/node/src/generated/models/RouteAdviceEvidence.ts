/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed traffic for the route over the window. Consumer fields are present only on throttle suggestions.
 */
export type RouteAdviceEvidence = {
  requests: number;
  anonymous_requests: number;
  server_errors: number;
  timeouts: number;
  cold_boots: number;
  p95_latency_ms: number;
  consumers?: number;
  top_consumer_id?: string;
  top_consumer_requests?: number;
  top_consumer_peak_per_minute?: number;
  next_consumer_peak_per_minute?: number;
};

