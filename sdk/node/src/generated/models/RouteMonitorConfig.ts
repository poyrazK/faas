/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorRoute } from './RouteMonitorRoute.js';
/**
 * Advisory production monitor intent, independent of the canary guard.
 */
export type RouteMonitorConfig = {
  app_id: string;
  enabled: boolean;
  /**
   * Saved request-time identity dimension used for per-cohort budget evaluation.
   */
  customer_group_by?: 'tenant' | 'consumer';
  /**
   * Present only when a confirmed error-budget incident that opens within 30 minutes of the deployment serving all traffic requests a checked rollback to the incident's healthy baseline (ADR-845). Omitted for the default report-only action.
   */
  on_violation?: 'rollback';
  revision: number;
  routes: Array<RouteMonitorRoute>;
  updated_at?: string;
};

