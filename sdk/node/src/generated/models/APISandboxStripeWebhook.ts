/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-only synthetic Stripe event fixture for an automated run.
 */
export type APISandboxStripeWebhook = {
  /**
   * Sandbox app route without a query or fragment.
   */
  delivery_path: string;
  event_type: string;
  /**
   * Fixture for Stripe data.object, up to 8 KiB as JSON.
   */
  data_object: Record<string, any>;
};

