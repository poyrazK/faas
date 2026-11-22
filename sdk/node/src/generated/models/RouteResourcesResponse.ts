/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteResourceAttribution } from './RouteResourceAttribution.js';
import type { RouteResourceSplit } from './RouteResourceSplit.js';
/**
 * Route attribution of an app's memory and CPU peaks over a window.
 */
export type RouteResourcesResponse = {
  slug: string;
  /**
   * Inclusive start of the analysed window.
   */
  from: string;
  /**
   * Exclusive end of the analysed window.
   */
  until: string;
  /**
   * The window held more minutes than one read analyses; the newest were used.
   */
  truncated: boolean;
  /**
   * Instance-minutes with memory and CPU samples.
   */
  resource_minutes: number;
  /**
   * Instance-minutes with samples that also served requests.
   */
  active_minutes: number;
  /**
   * Instance-minutes that served requests but have no samples; left out of every share.
   */
  route_minutes_without_resources: number;
  mem_peak_p50_mb: number;
  mem_peak_p95_mb: number;
  mem_peak_max_mb: number;
  /**
   * The app's memory limit; 0 when unknown.
   */
  mem_limit_mb: number;
  /**
   * Rise above the minute's starting memory that counted as a memory peak in this window.
   */
  high_memory_rise_mb: number;
  high_memory_minutes: number;
  /**
   * Memory peaks with no request in flight, such as background work.
   */
  unattributed_high_memory_minutes: number;
  cpu_peak_p95_millicores: number;
  /**
   * The app's CPU limit; 0 when unknown.
   */
  cpu_limit_millicores: number;
  high_cpu_minutes: number;
  routes: Array<RouteResourceAttribution>;
  /**
   * Routes past the 50 reported.
   */
  routes_omitted: number;
  split?: RouteResourceSplit;
};

