/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with consumer.signed_up once a customer's sign-up created their consumer (ADR-20261021183000). signup_id is stable across retries.
 */
export type APIConsumerSignedUpWebhookPayload = {
  signup_id: string;
  app_id: string;
  consumer_id: string;
  /**
   * The customer's Stripe customer.
   */
  external_ref: string;
  name: string;
  email: string;
  plan_id?: string;
  trial_ends_at?: string;
  livemode: boolean;
  signed_up_at: string;
};

