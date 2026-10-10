/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What-if estimate replayed from aggregated telemetry over the same window; observed_only, not a guarantee.
 */
export type RouteAdviceImpact = {
  /**
   * One-sentence what-if estimate for the suggested rule.
   */
  summary: string;
  estimated_cache_hits: number;
  estimated_wakes_avoided: number;
  estimated_throttled_requests: number;
  timeouts_affected: number;
};

