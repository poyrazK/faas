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
  /**
   * Opt-in maximum freshness for eligible bodyless GET responses. Zero disables caching; provider cache directives can shorten or prohibit storage.
   */
  response_cache_ttl_seconds?: number;
  /**
   * Consecutive transient provider failures before the integration circuit opens. Zero disables the breaker and requires circuit_breaker_open_seconds to be zero too.
   */
  circuit_breaker_failure_threshold?: number;
  /**
   * Cool-down after the breaker opens; after it elapses, only one cross-replica half-open provider probe is allowed. Must be set with a nonzero failure threshold.
   */
  circuit_breaker_open_seconds?: number;
};

