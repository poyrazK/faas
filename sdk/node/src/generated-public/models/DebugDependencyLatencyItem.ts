/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugDependencyQueryItem } from './DebugDependencyQueryItem.js';
/**
 * Bounded historical dependency aggregate. Percentiles are weighted by collapsed request-row counts and derived from sampled redacted spans.
 */
export type DebugDependencyLatencyItem = {
  type: 'application' | 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal' | 'app_dependency';
  kind?: string;
  name: string;
  calls: number;
  error_calls: number;
  error_rate_pct: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  /**
   * Wall time not covered by overlapping direct child spans.
   */
  exclusive_p50_ms?: number;
  /**
   * Wall time not covered by overlapping direct child spans.
   */
  exclusive_p95_ms?: number;
  /**
   * Wall time not covered by overlapping direct child spans.
   */
  exclusive_p99_ms?: number;
  baseline_p95_ms?: number;
  current_p95_ms?: number;
  p95_delta_ms?: number;
  baseline_exclusive_p95_ms?: number;
  current_exclusive_p95_ms?: number;
  exclusive_p95_delta_ms?: number;
  regression_factor?: number;
  /**
   * True when both split-window samples meet the minimum call threshold and current p95 is at least 1.5x and 25ms above baseline.
   */
  regression: boolean;
  baseline_error_rate_pct?: number;
  current_error_rate_pct?: number;
  error_rate_delta_pct?: number;
  /**
   * True when both split-window samples meet the minimum call threshold, the current side has at least 3 failed calls, and the current error rate is at least 5 percentage points and 2x above baseline.
   */
  failure_regression: boolean;
  /**
   * Most frequent span failure class, from the current side when it has failures.
   */
  top_error_type?: string;
  /**
   * Database dependencies only: the dependency broken down by normalized query pattern (literals, placeholders, IN lists and VALUES rows folded), regressions first.
   */
  queries?: Array<DebugDependencyQueryItem>;
  /**
   * True when more query patterns were seen than are returned.
   */
  queries_truncated?: boolean;
};

