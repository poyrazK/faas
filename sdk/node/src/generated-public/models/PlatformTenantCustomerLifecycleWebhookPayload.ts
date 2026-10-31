/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Active customer identity created for or linked to a platform tenant, or later revoked. The stable customer_external_ref joins identities across apps; consumer_id and app_id identify the app-local row.
 */
export type PlatformTenantCustomerLifecycleWebhookPayload = {
  platform_tenant_id: string;
  /**
   * The platform owner's stable customer reference.
   */
  external_ref: string;
  consumer_id: string;
  app_id: string;
  customer_external_ref: string;
  customer_name: string;
  customer_status: 'active' | 'revoked';
  changed_at: string;
};

