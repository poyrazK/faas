/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a receiver for this tenant's supported hostname, certificate, deployment, and finalized billing events.
 */
export type CreatePlatformTenantWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  /**
   * Events delivered to this receiver. Defaults to platform_tenant.statement.finalized when omitted.
   */
  event_filter?: Array<'platform_tenant.statement.finalized' | 'platform_tenant.hostname.verified' | 'platform_tenant.surface.certificate.changed' | 'platform_tenant.surface.deployment.changed'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

