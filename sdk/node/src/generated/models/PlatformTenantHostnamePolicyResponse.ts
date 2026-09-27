/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-managed allowlist and total hostname cap for downstream self-service. An absent policy is disabled.
 */
export type PlatformTenantHostnamePolicyResponse = {
  tenant_id: string;
  enabled: boolean;
  allowed_suffixes: Array<string>;
  max_hostnames: number;
  updated_at?: string;
};

