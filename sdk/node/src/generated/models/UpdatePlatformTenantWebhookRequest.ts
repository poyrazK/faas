/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Update a tenant statement receiver. Rotate secrets with the dedicated action.
 */
export type UpdatePlatformTenantWebhookRequest = {
  target_url?: string;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

