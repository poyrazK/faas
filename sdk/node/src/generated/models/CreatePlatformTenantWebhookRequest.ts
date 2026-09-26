/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a receiver for this tenant's finalized cross-app billing statements.
 */
export type CreatePlatformTenantWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

