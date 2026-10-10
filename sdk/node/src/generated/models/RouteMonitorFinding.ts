/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorRoute } from './RouteMonitorRoute.js';
import type { RouteMonitorWindow } from './RouteMonitorWindow.js';
/**
 * One production route and two windows with independently confirmed error and latency verdicts.
 */
export type RouteMonitorFinding = {
  route: RouteMonitorRoute;
  status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  reason: string;
  error_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  latency_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  windows: Array<RouteMonitorWindow>;
  /**
   * Present when the verdict comes from pooled_windows because the one-minute windows lacked requests (ADR-953). Budgets are unchanged.
   */
  evidence_window?: 'pooled';
  /**
   * Two consecutive halves of up to the newest 30 minutes since the observation anchor, read only for routes whose one-minute windows were sparse.
   */
  pooled_windows?: Array<RouteMonitorWindow>;
};

