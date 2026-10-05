/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthCounts } from './RouteHealthCounts.js';
/**
 * Observed weighted counts and independently evaluated absolute budgets in a closed window.
 */
export type RouteMonitorWindow = {
  start: string;
  end: string;
  observed: RouteHealthCounts;
  error_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  error_reason: string;
  latency_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  latency_reason: string;
};

