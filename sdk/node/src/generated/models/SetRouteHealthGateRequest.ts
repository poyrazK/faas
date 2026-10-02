/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthRoute } from './RouteHealthRoute.js';
export type SetRouteHealthGateRequest = {
  mode: 'report' | 'enforce';
  /**
   * Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for confirmed route 5xx regressions during an enforced canary; report mode is observational.
   */
  on_regression?: 'hold' | 'abort';
  expected_revision: number;
  routes: Array<RouteHealthRoute>;
};

