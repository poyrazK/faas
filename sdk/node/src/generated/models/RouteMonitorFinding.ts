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
};

