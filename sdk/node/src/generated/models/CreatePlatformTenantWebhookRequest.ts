/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a receiver for this tenant's supported customer, operation effect, hostname, certificate, deployment, reconciliation, and finalized billing events.
 */
export type CreatePlatformTenantWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  /**
   * Events delivered to this receiver. Defaults to platform_tenant.statement.finalized when omitted.
   */
  event_filter?: Array<'platform_tenant.statement.finalized' | 'platform_tenant.hostname.verified' | 'platform_tenant.surface.certificate.changed' | 'platform_tenant.surface.deployment.changed' | 'platform_tenant.customer.linked' | 'platform_tenant.customer.offboarded' | 'platform_tenant.reconciliation.applied' | 'operation.effect'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

