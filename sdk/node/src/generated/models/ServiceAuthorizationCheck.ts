/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable rule and sanitized reason from a service policy observation.
 */
export type ServiceAuthorizationCheck = {
  rule: string;
  result: 'allowed' | 'denied' | 'indeterminate';
  reason: string;
  setting?: string;
  hint?: string;
};

