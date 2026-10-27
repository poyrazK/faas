/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a provider-verified endpoint for Stripe or a custom sender using Gregale's timestamped HMAC-SHA256 protocol. Accepted events become durable app invocations or automation starts when bound.
 */
export type CreateInboundWebhookEndpointRequest = {
  name: string;
  provider: 'stripe' | 'generic';
  /**
   * Stripe signing secret or custom sender HMAC secret (at least 32 bytes for generic); sealed at rest and never returned.
   */
  signing_secret: string;
  delivery_path?: string;
  enabled?: boolean;
};

