/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable API consumer identity. No credential secret is returned. managed_by_platform_tenant distinguishes tenant-bundle-created identities from pre-existing linked identities.
 */
export type APIConsumerResponse = {
  id: string;
  app_id: string;
  external_ref: string;
  name: string;
  status: 'active' | 'revoked';
  created_at: string;
  updated_at: string;
  revoked_at?: string | null;
  /**
   * True only when a platform-tenant bundle created this consumer; omitted otherwise.
   */
  managed_by_platform_tenant?: boolean;
};

