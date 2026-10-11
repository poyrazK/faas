/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorCustomerImpact } from './RouteMonitorCustomerImpact.js';
import type { RouteMonitorIncidentTimelineRoute } from './RouteMonitorIncidentTimelineRoute.js';
/**
 * One confirmed production monitor evaluation. It contains no customer identifiers or request details.
 */
export type RouteMonitorIncidentTimelineEntry = {
  checked_at: string;
  coverage: 'observed_only';
  status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  reason: string;
  customer_impact?: RouteMonitorCustomerImpact;
  routes: Array<RouteMonitorIncidentTimelineRoute>;
};

