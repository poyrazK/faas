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
  windows: Array<RouteHealthWindowEvidence>;
};

