/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCriticalPathSpan } from './DebugCriticalPathSpan.js';
/**
 * Bounded causal path reconstructed from retained span timing and parent links.
 */
export type DebugRequestCriticalPath = {
  duration_ms: number;
  /**
   * True only when all retained spans have valid timing and parent links within the sample.
   */
  complete: boolean;
  span_count: number;
  slowest_span_id?: string;
  slowest_span_name?: string;
  slowest_exclusive_ms?: number;
  spans: Array<DebugCriticalPathSpan>;
};

