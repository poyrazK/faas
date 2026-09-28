/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional per-source gateway limit evaluated before consumer-key lookup, JWT verification, and VM wake. A gateway replica enforces its own buckets; the existing app/account limits remain aggregate ceilings. Observe mode records threshold crossings without rejecting requests.
 */
export type PreAuthRateLimitConfig = {
  mode: 'off' | 'observe' | 'enforce';
  /**
   * Required in observe/enforce mode and bounded by the app plan's request rate.
   */
  requests_per_second?: number;
  /**
   * Required in observe/enforce mode and bounded by the app plan's request burst.
   */
  burst?: number;
};

