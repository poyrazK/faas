/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed HTTP recovery for the exact restored deployment.
 */
export type ProjectRollbackTraffic = {
  status: 'awaiting_cutover' | 'awaiting_gateways' | 'awaiting_traffic' | 'verified' | 'superseded' | 'expired' | 'unavailable';
  route_generation?: number;
  routing_committed_at?: string;
  gateways_applied_at?: string;
  first_success_at?: string;
};

