/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugCompareRouteStats } from './DebugCompareRouteStats.js';
/**
 * POST response from /v1/apps/{slug}/debug/compare.
 */
export type DebugCompareResponse = {
  /**
   * App identity; present on servers supporting comparison-window metadata.
   */
  app_id?: string;
  /**
   * Effective lookback after the current retention cutoff; native default is 1h.
   */
  since?: string;
  window_start?: string;
  window_end?: string;
  /**
   * Whether the requested start was moved to the current plan-retention cutoff.
   */
  retention_clamped?: boolean;
  /**
   * Applied exact route filter; omitted for all routes.
   */
  route_filter?: string;
  source: string;
  mirror: string;
  routes: Array<DebugCompareRouteStats>;
};

