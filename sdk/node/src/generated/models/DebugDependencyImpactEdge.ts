/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCriticalPathSegment } from './DebugCriticalPathSegment.js';
/**
 * Bounded normalized parent-to-child dependency edge reconstructed from retained redacted span links.
 */
export type DebugDependencyImpactEdge = {
  from: DebugCriticalPathSegment;
  to: DebugCriticalPathSegment;
  calls: number;
  error_calls: number;
  error_rate_pct: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  exclusive_p50_ms: number;
  exclusive_p95_ms: number;
  exclusive_p99_ms: number;
  baseline_p95_ms?: number;
  current_p95_ms?: number;
  p95_delta_ms?: number;
  baseline_exclusive_p95_ms?: number;
  current_exclusive_p95_ms?: number;
  exclusive_p95_delta_ms?: number;
  regression_factor?: number;
  regression: boolean;
  baseline_error_rate_pct?: number;
  current_error_rate_pct?: number;
  error_rate_delta_pct?: number;
};

