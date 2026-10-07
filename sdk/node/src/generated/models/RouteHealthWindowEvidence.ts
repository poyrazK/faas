/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthCounts } from './RouteHealthCounts.js';
/**
 * Candidate and stable observations within one closed window, with independent error and latency verdicts.
 */
export type RouteHealthWindowEvidence = {
  start: string;
  end: string;
  candidate: RouteHealthCounts;
  stable: RouteHealthCounts;
  status: 'healthy' | 'regressed' | 'unknown';
  reason: string;
  error_status?: 'healthy' | 'regressed' | 'unknown';
  error_reason?: string;
  /**
   * Present when either latency check is selected.
   */
  latency_status?: 'healthy' | 'regressed' | 'unknown';
  latency_reason?: string;
  /**
   * Candidate p95 minus stable p95 in milliseconds when latency evidence is sufficient.
   */
  latency_delta_ms?: number;
  /**
   * Candidate p95 divided by stable p95. Omitted when stable p95 is zero or evidence is insufficient.
   */
  latency_factor?: number;
};

