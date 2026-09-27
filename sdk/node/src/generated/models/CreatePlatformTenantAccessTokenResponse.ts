/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Token metadata plus one-time plaintext bearer. Save the token securely; it is never retrievable again.
 */
export type CreatePlatformTenantAccessTokenResponse = {
  id: string;
  tenant_id: string;
  name: string;
  prefix: string;
  scopes: Array<'platform_tenant:usage:read' | 'platform_tenant:statements:read'>;
  created_at: string;
  expires_at: string;
  last_used_at?: string;
  revoked_at?: string;
  /**
   * One-time plaintext bearer; emitted only by create.
   */
  token: string;
};

