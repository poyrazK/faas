/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe endpoint metadata. endpoint_url is disclosed only by the create response.
 */
export type InboundWebhookEndpointResponse = {
  id: string;
  app_id: string;
  account_id: string;
  name: string;
  provider: 'stripe';
  delivery_path: string;
  enabled: boolean;
  signing_secret_masked: '***';
  /**
   * One-time-disclosed public provider URL, present only on create.
   */
  endpoint_url?: string;
  created_at: string;
  updated_at: string;
};

