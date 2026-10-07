/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorWindow } from './RouteMonitorWindow.js';
/**
 * One bounded request-time customer cohort; its UUID is returned only when customer_details=true.
 */
export type RouteMonitorCustomerCohort = {
  customer_id?: string;
  observed: boolean;
  status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  reason: string;
  error_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  latency_status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  windows: Array<RouteMonitorWindow>;
};

