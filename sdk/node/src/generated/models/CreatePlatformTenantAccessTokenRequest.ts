/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Mint a bearer for one downstream tenant. Plaintext is returned once and expires no later than one year after creation.
 */
export type CreatePlatformTenantAccessTokenRequest = {
  name: string;
  scopes: Array<'platform_tenant:usage:read' | 'platform_tenant:statements:read' | 'platform_tenant:activation:read' | 'platform_tenant:hostnames:manage' | 'platform_tenant:credentials:read' | 'platform_tenant:credentials:manage' | 'platform_tenant:consumers:manage' | 'platform_tenant:invocations:read' | 'platform_tenant:invocations:manage'>;
  /**
   * Defaults to 90 days from creation; maximum 365 days.
   */
  expires_at?: string;
};

