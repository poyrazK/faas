/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCriticalPathHistoryItem } from './DebugCriticalPathHistoryItem.js';
/**
 * Historical critical-path aggregates for one app. Raw span attributes and destinations are never exposed.
 */
export type DebugCriticalPathHistoryResponse = {
  app_id: string;
  since: string;
  window_start: string;
  window_end: string;
  retention_clamped: boolean;
  /**
   * True only when the row, path-cardinality, span-timing, and parent-link caps were not reached.
   */
  complete: boolean;
  truncated: boolean;
  telemetry_rows: number;
  represented_requests: number;
  path_samples: number;
  critical_paths: Array<DebugCriticalPathHistoryItem>;
};

