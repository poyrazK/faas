/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthRoute } from './RouteHealthRoute.js';
/**
 * Replacement critical-route health configuration and the revision the caller expects to update.
 */
export type SetRouteHealthGateRequest = {
  mode: 'report' | 'enforce';
  /**
   * Recovery action to save. Omission resets it to hold; abort requires enforce mode and two confirmed critical-route 5xx windows before automatic recovery.
   */
  on_regression?: 'hold' | 'abort';
  expected_revision: number;
  routes: Array<RouteHealthRoute>;
};

