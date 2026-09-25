/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Effective customer-selected per-integration policy, bounded by the account plan.
 */
export type OutboundRequestPolicy = {
  rate_per_second: number;
  burst: number;
  max_in_flight: number;
  request_timeout_ms: number;
};

