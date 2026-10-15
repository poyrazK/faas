/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Start or replace a route's model canary. Omitted margins take their defaults: 5 error-rate points, 50 calls, 30 minutes, and no latency margin.
 */
export type PutOutboundAIRouteCanaryRequest = {
  /**
   * The 1-based route position tried first.
   */
  target: number;
  percent: number;
  max_error_rate_increase?: number;
  max_latency_increase_percent?: number;
  min_calls?: number;
  window_minutes?: number;
};

