/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregated request analytics for one route and HTTP method. Counts include collapsed telemetry row weights.
 */
export type RequestAnalyticsRoute = {
  /**
   * Route template, not an expanded URL.
   */
  route: string;
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  requests: number;
  error_requests: number;
  error_rate_pct: number;
  cold_boots: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  /**
   * Estimated raw RAM-hour value allocated to this route by observed request share; excludes account-level included allowance and egress.
   */
  estimated_compute_cost_millicents?: number;
  request_share_pct?: number;
};

