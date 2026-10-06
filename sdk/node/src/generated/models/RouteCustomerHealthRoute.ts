/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCustomerHealthAttribution } from './RouteCustomerHealthAttribution.js';
import type { RouteCustomerHealthCohort } from './RouteCustomerHealthCohort.js';
/**
 * Exact selected route with bounded identity cohorts from the union of candidate and stable observations. Ranking uses candidate 5xx counts descending, then selected client response counts descending, combined request volume descending, and UUID ascending. Totals precede caps.
 */
export type RouteCustomerHealthRoute = {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  path: string;
  observed_customers: number;
  customers_truncated: boolean;
  candidate: RouteCustomerHealthAttribution;
  stable: RouteCustomerHealthAttribution;
  customers: Array<RouteCustomerHealthCohort>;
};

