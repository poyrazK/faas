/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * DNS ownership verification for a hostname on a surface explicitly linked to this tenant. It does not imply certificate issuance or routing, and never contains the DNS challenge token.
 */
export type PlatformTenantHostnameVerifiedWebhookPayload = {
  platform_tenant_id: string;
  /**
   * Stable customer reference selected by the platform owner.
   */
  external_ref: string;
  surface_id: string;
  surface_name: string;
  app_id: string;
  hostname_id: string;
  hostname: string;
  verified_at: string;
};

