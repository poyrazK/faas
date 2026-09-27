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
  /**
   * Extra attempts for bodyless GET/HEAD requests after selected transient failures. Retries share the request timeout and count as one admission.
   */
  max_retries?: number;
};

