/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PATCH body for `/v1/apps/{slug}/security`. Both fields are optional
 * so an operator can change signature enforcement and deploy posture
 * independently.
 *
 */
export type AppSecurityRequest = {
  /**
   * Operator-only toggle. nil = no change. *true = opt in to signature enforcement (requires the trust list to be non-empty). *false = opt out.
   */
  require_signed?: boolean | null;
  /**
   * Deploy-time response to high-severity configuration findings. off = advisory report only; warn = non-blocking; enforce = reject new deploys until remediated and require a trusted signature for OCI images.
   */
  security_policy?: 'off' | 'warn' | 'enforce';
};

