/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthClientErrorReport } from './RouteHealthClientErrorReport.js';
import type { RouteHealthWindowEvidence } from './RouteHealthWindowEvidence.js';
/**
 * Combined verdict and both closed-window evidence records for one selected critical route.
 */
export type RouteHealthFinding = {
  client_errors?: RouteHealthClientErrorReport;
  /**
   * Configured watched response codes for this exact route. Live client_errors contains their independent advisory evidence; saved decisions retain selectors without code evidence.
   */
  watch_statuses?: Array<401 | 403 | 404 | 422 | 429>;
  method: string;
  path: string;
  /**
   * Whether the relative p95 slowdown check is selected.
   */
  check_latency?: boolean;
  /**
   * Absolute candidate p95 budget in milliseconds; zero or omitted disables this check.
   */
  max_p95_ms?: number;
  status: 'healthy' | 'regressed' | 'unknown';
  reason: string;
  error_status?: 'healthy' | 'regressed' | 'unknown';
  error_reason?: string;
  /**
   * Independently confirmed latency verdict across both windows when selected.
   */
  latency_status?: 'healthy' | 'regressed' | 'unknown';
  latency_reason?: string;
  /**
   * pooled when the verdict comes from pooled_windows because the one-minute windows lacked requests (ADR-944); synthetic when it comes from synthetic_windows because organic evidence stayed sparse (ADR-945). Thresholds are unchanged.
   */
  evidence_window?: 'pooled' | 'synthetic';
  windows: Array<RouteHealthWindowEvidence>;
  /**
   * Two consecutive halves of up to the newest 30 minutes of the stage, read only for routes whose one-minute windows were sparse.
   */
  pooled_windows?: Array<RouteHealthWindowEvidence>;
  /**
   * Synthetic probe results over the pooled bounds for probed selectors that organic evidence left sparse. They settle the 5xx signal only.
   */
  synthetic_windows?: Array<RouteHealthWindowEvidence>;
};

