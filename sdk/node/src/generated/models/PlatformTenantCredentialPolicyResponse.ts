/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-configured scope allowlist and active-key ceiling for delegated credential management.
 */
export type PlatformTenantCredentialPolicyResponse = {
  tenant_id: string;
  enabled: boolean;
  allowed_scopes: Array<'read' | 'write' | 'admin'>;
  max_keys_per_consumer: number;
  updated_at?: string;
};

