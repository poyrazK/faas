/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Tenant-scoped event receiver. The secret is never returned.
 */
export type PlatformTenantWebhookResponse = {
  id: string;
  scope: 'platform_tenant';
  platform_tenant_id: string;
  account_id: string;
  target_url: string;
  webhook_secret_sealed_masked: '***';
  event_filter: Array<'platform_tenant.statement.finalized' | 'platform_tenant.hostname.verified' | 'platform_tenant.surface.certificate.changed' | 'platform_tenant.surface.deployment.changed' | 'platform_tenant.customer.linked' | 'platform_tenant.customer.offboarded'>;
  retry_policy: 'default' | 'aggressive' | 'none';
  delivery_format: 'json' | 'cloudevents';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

