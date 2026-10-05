/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthFinding } from './RouteHealthFinding.js';
/**
 * One observed tenant or API consumer, compared independently with the same route thresholds and windows. Sparse or one-sided evidence is unknown.
 */
export type RouteCustomerHealthCohort = {
  /**
   * Present only when customer_details=true. Identity names and external references are excluded.
   */
  customer_id?: string;
  health: RouteHealthFinding;
};

