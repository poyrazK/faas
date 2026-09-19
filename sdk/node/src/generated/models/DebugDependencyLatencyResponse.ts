/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugDependencyLatencyItem } from './DebugDependencyLatencyItem.js';
/**
 * Historical dependency latency for one app. Raw span attributes and destinations are never exposed.
 */
export type DebugDependencyLatencyResponse = {
  app_id: string;
  since: string;
  window_start: string;
  window_end: string;
  retention_clamped: boolean;
  /**
   * True only when the row, span, and dependency-cardinality caps were not reached.
   */
  complete: boolean;
  truncated: boolean;
  telemetry_rows: number;
  represented_requests: number;
  span_samples: number;
  dependencies: Array<DebugDependencyLatencyItem>;
};

