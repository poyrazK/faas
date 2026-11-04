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
  /**
   * Anonymous GET responses the gateways compared with the previous response for their URL. Cache suggestions only.
   */
  content_observed?: number;
  /**
   * Observed responses identical to the previous one for their URL within the longest content window (1 h).
   */
  content_repeats?: number;
  /**
   * Observed responses that differed from the previous one for their URL within the longest content window.
   */
  content_changes?: number;
  /**
   * Observed responses the edge cache could not store (Set-Cookie, Cache-Control no-store or private, Vary, or size).
   */
  content_uncacheable?: number;
  /**
   * Longest cache lifetime at which the observed repeats would have served at most 1% stale bodies; absent until enough repeats are measured.
   */
  safe_max_age_seconds?: 60 | 300 | 3600;
};

