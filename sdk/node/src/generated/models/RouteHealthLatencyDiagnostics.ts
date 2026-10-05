/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthDependencyComparison } from './RouteHealthDependencyComparison.js';
import type { RouteHealthLatencySample } from './RouteHealthLatencySample.js';
/**
 * Newest 32 retained rows per deployment/window, including rows without spans. Normalized type/kind groups exclude names, SQL and attributes. Sample percentiles are not additive and do not establish cause or complete capture.
 */
export type RouteHealthLatencyDiagnostics = {
  coverage: 'retained_samples';
  rows_limit: number;
  dependencies_truncated: boolean;
  candidate: RouteHealthLatencySample;
  stable: RouteHealthLatencySample;
  dependencies: Array<RouteHealthDependencyComparison>;
};

