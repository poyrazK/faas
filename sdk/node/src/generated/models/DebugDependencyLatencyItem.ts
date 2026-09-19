/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded historical dependency aggregate. Percentiles are weighted by collapsed request-row counts and derived from sampled redacted spans.
 */
export type DebugDependencyLatencyItem = {
  type: 'application' | 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal';
  kind?: string;
  name: string;
  calls: number;
  error_calls: number;
  error_rate_pct: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  baseline_p95_ms?: number;
  current_p95_ms?: number;
  p95_delta_ms?: number;
  regression_factor?: number;
  /**
   * True when both split-window samples meet the minimum call threshold and current p95 is at least 1.5x and 25ms above baseline.
   */
  regression: boolean;
  baseline_error_rate_pct?: number;
  current_error_rate_pct?: number;
  error_rate_delta_pct?: number;
};

