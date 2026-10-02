/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RouteHealthCounts = {
  requests: number;
  server_errors: number;
  error_rate: number;
  /**
   * Weighted p95 estimate in milliseconds from collapsed telemetry representatives. Omitted when latency is not selected or evidence is unavailable; zero is a valid observation.
   */
  p95_latency_ms?: number;
};

