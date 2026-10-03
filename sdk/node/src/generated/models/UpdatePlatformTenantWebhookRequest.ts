/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Update a tenant event receiver. Its event filter is immutable; create a replacement to change subscribed events.
 */
export type UpdatePlatformTenantWebhookRequest = {
  target_url?: string;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

