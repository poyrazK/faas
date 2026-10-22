/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deploy-time security posture requirement. Modes and override combinations are validated on publication.
 */
export type ApplicationStandardSecurityPolicyRule = {
  mode: 'default' | 'mandatory' | 'restricted';
  override?: 'none' | 'narrow';
  value: 'off' | 'warn' | 'enforce';
};

