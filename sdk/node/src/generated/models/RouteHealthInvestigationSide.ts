/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthInvestigationExample } from './RouteHealthInvestigationExample.js';
/**
 * Matching response weights and row inventory for one deployment/window, counted before the example cap. Examples prioritize trace-linked rows, then newest timestamp and descending telemetry UUID.
 */
export type RouteHealthInvestigationSide = {
  matching_requests: number;
  observed_rows: number;
  examples_truncated: boolean;
  examples: Array<RouteHealthInvestigationExample>;
};

