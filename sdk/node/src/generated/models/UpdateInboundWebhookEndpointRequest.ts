/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Omitted fields remain unchanged. A signing_secret value rotates the provider secret in place; generic secrets must contain at least 32 bytes.
 */
export type UpdateInboundWebhookEndpointRequest = {
  /**
   * Generic provider secrets must contain at least 32 bytes.
   */
  signing_secret?: string;
  delivery_path?: string;
  enabled?: boolean;
};

