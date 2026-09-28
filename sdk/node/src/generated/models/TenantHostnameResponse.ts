/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A hostname attached to a tenant surface (DNS-01 verified). managed_by_platform_tenant is true only when created by a platform-tenant bundle apply.
 */
export type TenantHostnameResponse = {
  hostname: string;
  challenge_token?: string | null;
  verified: boolean;
  verified_at?: string | null;
  last_error?: string | null;
  /**
   * TXT record the customer must publish (_faas-verify.<hostname>).
   */
  txt_record: string;
  /**
   * True only when a platform-tenant bundle created this hostname; omitted otherwise.
   */
  managed_by_platform_tenant?: boolean;
};

