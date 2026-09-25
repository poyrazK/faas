/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsDependency } from './RequestAnalyticsDependency.js';
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
   * Weighted p95 gateway-observed request duration among requests that woke a cold instance. Includes handler time; not an isolated wake-phase duration.
   */
  cold_request_p95_ms?: number | null;
  /**
   * p95 from schedd wake.boot_started to wake.boot_completed (instance RUNNING) for distinct wake IDs correlated to this route; null when event pairs are unavailable.
   */
  wake_boot_p95_ms?: number | null;
  /**
   * Weighted p50 wall time from platform-runner execution evidence; not CPU time. Null when runtime evidence is unavailable.
   */
  guest_execution_p50_ms?: number | null;
  /**
   * Weighted p95 wall time from platform-runner execution evidence; not CPU time. Null when runtime evidence is unavailable.
   */
  guest_execution_p95_ms?: number | null;
  /**
   * Count of retained, platform-classified dependency span samples for this route; not a complete call count.
   */
  dependency_samples?: number;
  /**
   * Collapsed request weight represented by rows with at least one classified dependency span.
   */
  dependency_requests?: number;
  /**
   * Top classified dependencies ranked by sampled exclusive p95. Values are bounded and sample-based.
   */
  dependencies?: Array<RequestAnalyticsDependency>;
  /**
   * Estimated raw RAM-hour value allocated to this route by observed request share; excludes account-level included allowance and egress.
   */
  estimated_compute_cost_millicents?: number;
  request_share_pct?: number;
};

