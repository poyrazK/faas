/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugSuspectedDependency } from './DebugSuspectedDependency.js';
/**
 * One regression observation row.
 */
export type DebugRegressionItem = {
  deployment_id: string;
  route: string;
  /**
   * Which signal regressed (absent from servers before error-rate detection, meaning latency): latency (p95 at least 1.2x baseline across at least 100 slower requests), errors (the share of HTTP 5xx responses rose at least 5 percentage points and 2x, with at least 10 failed and 50 total requests on each side), or both.
   */
  kind?: 'latency' | 'errors' | 'latency_and_errors';
  p95_ms: number;
  p95_base_ms: number;
  /**
   * Requests slower than the latency threshold; for an errors-only observation, the failed requests.
   */
  affected_count: number;
  /**
   * Decimal string with up to 2 places, NUMERIC(5,2). Latency inflation; 1.00 for an errors-only observation whose p95 did not rise.
   */
  regression_factor: string;
  /**
   * Share of HTTP 5xx responses on the regressed deployment's route.
   */
  error_rate_pct?: number;
  /**
   * Share of HTTP 5xx responses on the previous deployment's route.
   */
  error_rate_base_pct?: number;
  /**
   * HTTP 5xx responses on the regressed deployment's route in the detection window.
   */
  error_count?: number;
  first_detected_at: string;
  last_detected_at: string;
  state: 'active' | 'acknowledged' | 'dismissed' | 'resolved';
  acknowledged_at?: string;
  dismissed_until?: string;
  resolved_at?: string;
  suspected_dependency?: DebugSuspectedDependency;
};

