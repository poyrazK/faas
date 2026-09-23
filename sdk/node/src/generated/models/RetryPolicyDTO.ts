/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
 * is a requested total-attempt count; zero inherits the applicable
 * account plan and never means unlimited. Durable invocation
 * producers materialize the effective plan-capped value, and the
 * scheduler re-clamps it at dispatch time to account for later plan
 * downgrades. Lives in pkg/api so the SDK can type the policy
 * without importing pkg/dispatch directly.
 *
 */
export type RetryPolicyDTO = {
  /**
   * Total delivery attempts including the original. 0 inherits the current plan cap; it never means unlimited.
   */
  max_attempts?: number;
  base_seconds?: number;
  max_seconds?: number;
  /**
   * Fraction (0..1) added to the backoff delay. 0.2 means ±20% jitter on top of the exponential curve.
   */
  jitter_seconds?: number;
};

