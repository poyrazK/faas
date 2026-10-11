/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorFinding } from './RouteMonitorFinding.js';
/**
 * A route/signal that changed from non-violated to violated. Route indexes refer to opening_report.routes.
 */
export type RouteMonitorIncidentEscalationSignal = {
  route_index: number;
  signal: 'errors' | 'latency';
  finding: RouteMonitorFinding;
};

