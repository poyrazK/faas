/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorRoute } from './RouteMonitorRoute.js';
/**
 * Proposed route monitor intent evaluated against recent production observations without being saved.
 */
export type PreviewRouteMonitorRequest = {
  /**
   * Optional request-time identity dimension for per-cohort evaluation.
   */
  customer_group_by?: 'tenant' | 'consumer';
  routes: Array<RouteMonitorRoute>;
};

