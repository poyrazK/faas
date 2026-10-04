/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorCustomerCohort } from './RouteMonitorCustomerCohort.js';
import type { RouteMonitorCustomerWindow } from './RouteMonitorCustomerWindow.js';
/**
 * Full-population route counts plus at most five displayed cohorts and one hundred violating identities.
 */
export type RouteMonitorCustomerRoute = {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  path: string;
  observed_customers: number;
  violated_customers: number;
  unknown_customers: number;
  recovery_missing_customers: number;
  recovery_remaining_customers: number;
  customers_truncated: boolean;
  violating_customers_truncated: boolean;
  /**
   * Present only when customer_details=true.
   */
  violating_customer_ids?: Array<string>;
  windows: Array<RouteMonitorCustomerWindow>;
  customers: Array<RouteMonitorCustomerCohort>;
};

