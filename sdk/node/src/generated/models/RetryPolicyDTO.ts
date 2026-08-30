/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. The handler
 * decodes this DTO into a dispatch.RetryPolicy before persisting
 * to invocations.retry_policy JSONB. Lives in pkg/api so the SDK
 * can type the override without importing pkg/dispatch directly.
 *
 */
export type RetryPolicyDTO = {
  max_attempts?: number;
  base_seconds?: number;
  max_seconds?: number;
  /**
   * Fraction (0..1) added to the backoff delay. 0.2 means ±20% jitter on top of the exponential curve.
   */
  jitter_seconds?: number;
};

