/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a provider-verified endpoint whose accepted events become durable app invocations.
 */
export type CreateInboundWebhookEndpointRequest = {
  name: string;
  provider: 'stripe';
  /**
   * Provider endpoint secret; sealed at rest and never returned.
   */
  signing_secret: string;
  delivery_path?: string;
  enabled?: boolean;
};

