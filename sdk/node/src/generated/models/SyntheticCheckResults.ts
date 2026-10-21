/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SyntheticCheckRun } from './SyntheticCheckRun.js';
/**
 * Recent outcomes of one synthetic check, returned by GET /v1/apps/{slug}/synthetics/{id} (ADR-748).
 */
export type SyntheticCheckResults = {
  /**
   * Share of runs in the last 24 hours that succeeded; null when none ran.
   */
  uptime_24h_pct: number | null;
  /**
   * Share of runs in the last 7 days that succeeded; null when none ran.
   */
  uptime_7d_pct: number | null;
  /**
   * Runs in the last 24 hours.
   */
  runs_24h: number;
  /**
   * 95th-percentile latency of successful runs in the last 24 hours, including any wake; 0 when none succeeded.
   */
  p95_latency_ms_24h: number;
  /**
   * The most recent runs, newest first (at most 20).
   */
  recent: Array<SyntheticCheckRun>;
};

