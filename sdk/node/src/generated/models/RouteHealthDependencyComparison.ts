/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthDependencyTiming } from './RouteHealthDependencyTiming.js';
/**
 * Retained dependency comparison. Deltas require measurements from both deployments. Ordered by positive comparable p95 change, candidate p95, type and kind; at most 16 groups.
 */
export type RouteHealthDependencyComparison = {
  type: 'application' | 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal' | 'app_dependency';
  kind?: string;
  status: 'compared' | 'one_sided';
  candidate: RouteHealthDependencyTiming;
  stable: RouteHealthDependencyTiming;
  p95_delta_ms?: number;
  exclusive_p95_delta_ms?: number;
};

