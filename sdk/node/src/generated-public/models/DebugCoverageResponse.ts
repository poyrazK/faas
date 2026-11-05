/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCoverageSignal } from './DebugCoverageSignal.js';
/**
 * Customer-safe debugger coverage summary. This is observed coverage
 * of retained telemetry, not a claim that every gateway request was
 * persisted.
 *
 */
export type DebugCoverageResponse = {
  app_id: string;
  since: string;
  window_start: string;
  window_end: string;
  plan_retention_days: number;
  telemetry_rows: number;
  represented_requests: number;
  error_requests: number;
  trace_linked: DebugCoverageSignal;
  span_evidence: DebugCoverageSignal;
  wake_evidence: DebugCoverageSignal;
  guest_evidence: DebugCoverageSignal;
  oldest_telemetry_at?: string;
  latest_telemetry_at?: string;
};

