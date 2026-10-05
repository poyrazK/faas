/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthInvestigationExample } from './RouteHealthInvestigationExample.js';
/**
 * Weighted nearest-rank p95 of retained spans. Calls use collapsed request weights and can exceed request counts with multiple spans. Exclusive p95 subtracts overlapping direct children; omitted if timing is incomplete or spans are capped. Examples are bounded request references.
 */
export type RouteHealthDependencyTiming = {
  span_samples: number;
  represented_calls: number;
  error_calls: number;
  p95_ms?: number;
  exclusive_p95_ms?: number;
  examples: Array<RouteHealthInvestigationExample>;
};

