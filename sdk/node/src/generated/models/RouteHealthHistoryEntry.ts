/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthDecision } from './RouteHealthDecision.js';
import type { RouteHealthEvaluationPolicy } from './RouteHealthEvaluationPolicy.js';
import type { RouteHealthReport } from './RouteHealthReport.js';
/**
 * Immutable evidence from a real canary advance evaluation or committed automatic route health abort, bounded to 64 KiB encoded JSON. Identical advance retries retain the original id and checked_at.
 */
export type RouteHealthHistoryEntry = {
  version: 1;
  id: string;
  checked_at: string;
  source: 'manual' | 'worker';
  /**
   * Abort identifies a committed automatic rollback with worker source and requested traffic 0. Omitted for advance evaluations.
   */
  purpose?: 'abort';
  /**
   * Candidate traffic before evaluation.
   */
  traffic_percent: number;
  requested_traffic_percent: number;
  policy: RouteHealthEvaluationPolicy;
  decision: RouteHealthDecision;
  report: RouteHealthReport;
};

