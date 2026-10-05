/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorCustomerRoute } from './RouteMonitorCustomerRoute.js';
/**
 * Request-time tenant or API-consumer budget summary. Identity details are redacted unless explicitly requested.
 */
export type RouteMonitorCustomerReport = {
  group_by: 'tenant' | 'consumer';
  details_included: boolean;
  coverage: 'observed_only';
  status: 'healthy' | 'violated' | 'unknown' | 'disabled';
  reason: string;
  customers_limit: number;
  observed_customers: number;
  violated_customers: number;
  unknown_customers: number;
  /**
   * Distinct known violating identities not yet observed healthy again.
   */
  recovery_remaining_customers: number;
  recovery_inventory_incomplete: boolean;
  routes: Array<RouteMonitorCustomerRoute>;
};

