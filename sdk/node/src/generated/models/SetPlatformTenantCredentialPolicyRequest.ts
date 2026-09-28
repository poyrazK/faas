/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit scopes and per-consumer key ceiling for tenant-bound credential self-service; empty scopes and zero disable it.
 */
export type SetPlatformTenantCredentialPolicyRequest = {
  allowed_scopes: Array<'read' | 'write' | 'admin'>;
  max_keys_per_consumer: number;
};

