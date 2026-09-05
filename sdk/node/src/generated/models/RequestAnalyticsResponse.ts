/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsRoute } from './RequestAnalyticsRoute.js';
/**
 * Bounded historical request analytics for
 * `GET /v1/apps/{slug}/analytics?since=`. The window is half-open
 * `[from, until)`, and the route list is capped at 50 rows.
 *
 */
export type RequestAnalyticsResponse = {
  slug: string;
  /**
   * Effective lookback duration after retention clamping.
   */
  since: string;
  /**
   * Inclusive lower bound of the analytics window.
   */
  from: string;
  /**
   * Exclusive upper bound of the analytics window.
   */
  until: string;
  /**
   * True when the requested lookback exceeded plan retention.
   */
  window_clamped: boolean;
  requests: number;
  error_requests: number;
  error_rate_pct: number;
  cold_boots: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  routes: Array<RequestAnalyticsRoute>;
  /**
   * Maximum number of route rows returned.
   */
  routes_limit: number;
  /**
   * True when more route rows matched than routes_limit.
   */
  routes_truncated: boolean;
  /**
   * RFC3339Nano UTC assembly timestamp.
   */
  as_of: string;
};

