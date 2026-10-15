/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorRoute } from './RouteMonitorRoute.js';
/**
 * Compare-and-swap update of the selected environment route-budget override.
 */
export type SetEnvironmentRouteBudgetsRequest = {
  app_id: string;
  project_id?: string;
  environment_id?: string;
  request_id: string;
  expected_revision: number;
  expected_app_revision: number;
  /**
   * Requires enabled=false and empty routes; restores current app thresholds.
   */
  inherit: boolean;
  enabled: boolean;
  routes: Array<RouteMonitorRoute>;
};

