/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An organization's security policy.
 */
export type OrgSecurityPolicy = {
  /**
   * Members without two-factor authentication are refused on the organization's routes.
   */
  require_mfa: boolean;
  readonly updated_at?: string;
};

