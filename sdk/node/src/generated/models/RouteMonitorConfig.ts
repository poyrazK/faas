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
  revision: number;
  routes: Array<RouteMonitorRoute>;
  updated_at?: string;
};

