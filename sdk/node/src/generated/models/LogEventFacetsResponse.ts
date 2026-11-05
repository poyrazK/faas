/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { LogFacetCount } from './LogFacetCount.js';
/**
 * Log event counts by facet over one window.
 */
export type LogEventFacetsResponse = {
  /**
   * Facet window start after clamping to retention.
   */
  since: string;
  until: string;
  total: number;
  sources: Array<LogFacetCount>;
  levels: Array<LogFacetCount>;
  status_classes: Array<LogFacetCount>;
  routes: Array<LogFacetCount>;
  /**
   * More routes matched than the ten returned.
   */
  routes_truncated: boolean;
};

