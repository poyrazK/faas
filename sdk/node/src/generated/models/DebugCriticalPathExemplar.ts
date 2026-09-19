/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCriticalPathSegment } from './DebugCriticalPathSegment.js';
/**
 * Bounded representative request for a historical critical path. Request identifiers resolve only to retained redacted debugger evidence.
 */
export type DebugCriticalPathExemplar = {
  request_id: string;
  trace_id?: string;
  window: 'baseline' | 'current';
  received_at: string;
  duration_ms: number;
  http_status: number;
  error: boolean;
  count: number;
  dominant_segment?: DebugCriticalPathSegment;
  dominant_segment_exclusive_ms?: number;
};

