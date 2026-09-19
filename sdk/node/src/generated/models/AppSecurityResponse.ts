/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PATCH /v1/apps/{slug}/security response body — the updated security controls.
 */
export type AppSecurityResponse = {
  /**
   * The current state of the require_signed flag after the patch.
   */
  require_signed: boolean;
  /**
   * The current deploy-time posture policy after the patch.
   */
  security_policy: 'off' | 'warn' | 'enforce';
};

