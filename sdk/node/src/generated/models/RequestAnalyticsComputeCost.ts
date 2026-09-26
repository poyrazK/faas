/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Estimated app compute value over the analytics window, allocated by observed request share. This is not an invoice amount; it values raw RAM-hours before the account's included allowance and excludes egress.
 */
export type RequestAnalyticsComputeCost = {
  estimated_millicents: number;
  allocated_millicents: number;
  /**
   * Estimated compute value not assigned because the window has no observed route requests.
   */
  unallocated_millicents: number;
  /**
   * Value allocated to requests outside the top route list.
   */
  other_route_millicents: number;
  other_route_requests: number;
  other_route_request_share_pct: number;
  rate_millicents_per_gb_hour: number;
  currency: 'EUR';
  allocation_method: 'request_share';
  basis: 'raw_ram_hours_at_current_overage_rate_before_allowance';
  request_count: number;
};

