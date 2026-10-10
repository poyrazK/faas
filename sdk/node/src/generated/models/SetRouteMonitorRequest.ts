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
  /**
   * report (default) only records incidents. rollback requests one checked rollback per early error-budget incident and requires at least one route with max_5xx_rate_bps.
   */
  on_violation?: 'report' | 'rollback';
  expected_revision: number;
  routes: Array<RouteMonitorRoute>;
};

