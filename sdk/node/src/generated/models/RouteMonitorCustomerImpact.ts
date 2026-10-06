/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregate observed request-time identity counts; never includes customer IDs.
 */
export type RouteMonitorCustomerImpact = {
  group_by: 'tenant' | 'consumer';
  coverage: 'observed_only';
  observed_customers: number;
  violated_customers: number;
};

