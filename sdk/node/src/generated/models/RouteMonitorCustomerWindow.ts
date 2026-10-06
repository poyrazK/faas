/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request-time identity attribution totals for one monitored window. These are observed telemetry counts, not unique people or billing data.
 */
export type RouteMonitorCustomerWindow = {
  start: string;
  end: string;
  identified_requests: number;
  unattributed_requests: number;
  unresolved_identity_requests: number;
  /**
   * Identified requests belonging to cohorts outside the displayed five.
   */
  other_customer_requests: number;
};

