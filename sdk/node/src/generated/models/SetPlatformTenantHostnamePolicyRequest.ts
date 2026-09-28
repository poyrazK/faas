/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replace the tenant's domain delegation. Empty suffixes and zero max_hostnames disable tenant self-service.
 */
export type SetPlatformTenantHostnamePolicyRequest = {
  allowed_suffixes: Array<string>;
  max_hostnames: number;
};

