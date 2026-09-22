/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update of an upstream's egress circuit-breaker policy
 * (ADR-201 §3). Omitted fields are left unchanged.
 *
 * Setting `enabled` to false leaves the threshold fields intact, so
 * toggling protection off does not discard your tuning.
 *
 */
export type UpdateUpstreamCircuitBreakerRequest = {
  /**
   * Turn egress breaking on or off for this upstream.
   */
  enabled?: boolean;
  failure_threshold?: number;
  min_samples?: number;
  open_seconds?: number;
};

