/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCustomerUsage } from './RouteCustomerUsage.js';
/**
 * Bounded observed customer usage for one app, deployment and half-open retained telemetry window.
 */
export type RouteCustomerUsageResponse = {
  /**
   * App whose deployment and telemetry were read.
   */
  slug: string;
  /**
   * Selected immutable deployment owned by this app.
   */
  deployment_id: string;
  /**
   * Effective inclusive lower bound after retention clamping.
   */
  from: string;
  /**
   * Exclusive upper bound of the observation window.
   */
  until: string;
  /**
   * UTC time at which the read window was assembled.
   */
  as_of: string;
  /**
   * Requested start was older than current retained telemetry.
   */
  window_clamped: boolean;
  /**
   * Complete capture cannot be established from retained telemetry.
   */
  coverage: 'observed_only';
  /**
   * Top route/method observations ordered by weighted requests, route, then method.
   */
  routes: Array<RouteCustomerUsage>;
  /**
   * Maximum route observations returned.
   */
  routes_limit: number;
  /**
   * More route observations matched than the route limit; missing routes remain unknown.
   */
  routes_truncated: boolean;
  /**
   * Maximum identity groups returned per route; aggregate counts precede this cap.
   */
  customers_limit: number;
};

