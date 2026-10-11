/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorCustomerImpact } from './RouteMonitorCustomerImpact.js';
/**
 * Status for one route in the incident's fixed opening selector order.
 */
export type RouteMonitorIncidentTimelineRoute = {
  route_index: number;
  status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  error_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  latency_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  customer_impact?: RouteMonitorCustomerImpact;
};

