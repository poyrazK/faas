/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsComputeCost } from './RequestAnalyticsComputeCost.js';
import type { RequestAnalyticsDeploymentCostBreakdown } from './RequestAnalyticsDeploymentCostBreakdown.js';
import type { RequestAnalyticsGroup } from './RequestAnalyticsGroup.js';
import type { RequestAnalyticsRoute } from './RequestAnalyticsRoute.js';
import type { RequestAnalyticsTelemetryGap } from './RequestAnalyticsTelemetryGap.js';
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
  group_by: 'route' | 'country' | 'referrer_host' | 'ua_family' | 'status' | 'consumer_id';
  groups: Array<RequestAnalyticsGroup>;
  /**
   * Maximum number of top groups before __other__.
   */
  groups_limit: number;
  /**
   * True when __other__ contains groups outside the top-N.
   */
  groups_truncated: boolean;
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
   * True when dependency span rows, route dependency groups, or route dependency outputs were capped.
   */
  dependencies_truncated: boolean;
  compute_cost?: RequestAnalyticsComputeCost;
  deployment_costs?: RequestAnalyticsDeploymentCostBreakdown;
  /**
   * RFC3339Nano UTC assembly timestamp.
   */
  as_of: string;
  /**
   * True when every gateway delivered this window's request telemetry, so the totals include every request.
   * Judged up to two minutes before `as_of`, because the newest minutes are still arriving. Absent when it could not be judged.
   *
   */
  telemetry_complete?: boolean;
  /**
   * Why requests in this window may be missing from the totals. Empty or absent when the window is complete.
   */
  telemetry_gaps?: Array<RequestAnalyticsTelemetryGap>;
  /**
   * Whether the detail only sampled raw rows carry is in this response: group CPU and sampled latency,
   * `deployment_costs`, and per-route deployment splits. `omitted` when reading it failed or ran past its
   * time budget; request counts, error rates and percentiles never depend on it.
   *
   */
  sampled_measures?: 'included' | 'omitted';
};

