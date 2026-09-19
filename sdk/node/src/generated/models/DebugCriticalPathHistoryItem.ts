/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCriticalPathExemplar } from './DebugCriticalPathExemplar.js';
import type { DebugCriticalPathSegment } from './DebugCriticalPathSegment.js';
/**
 * Bounded historical critical-path aggregate. Percentiles are weighted by collapsed request-row counts and derived from sampled redacted spans.
 */
export type DebugCriticalPathHistoryItem = {
  signature: string;
  segments: Array<DebugCriticalPathSegment>;
  dominant_segment?: DebugCriticalPathSegment;
  dominant_segment_exclusive_ms?: number;
  exemplars: Array<DebugCriticalPathExemplar>;
  calls: number;
  error_calls: number;
  error_rate_pct: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  baseline_calls: number;
  current_calls: number;
  baseline_p95_ms: number;
  current_p95_ms: number;
  p95_delta_ms: number;
  regression_factor: number;
  /**
   * True when both path samples meet the minimum threshold and the newer p95 exceeds the older p95 by the configured factor and delta.
   */
  regression: boolean;
  baseline_error_rate_pct: number;
  current_error_rate_pct: number;
  error_rate_delta_pct: number;
};

