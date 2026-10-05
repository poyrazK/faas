/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorRoute } from './RouteMonitorRoute.js';
/**
 * Replacement production monitor intent, with a mandatory revision check.
 */
export type SetRouteMonitorRequest = {
  enabled: boolean;
  /**
   * Optional request-time identity dimension. Omission disables per-cohort evaluation.
   */
  customer_group_by?: 'tenant' | 'consumer';
  expected_revision: number;
  routes: Array<RouteMonitorRoute>;
};

