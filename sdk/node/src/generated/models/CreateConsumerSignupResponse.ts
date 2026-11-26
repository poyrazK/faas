/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A started sign-up. Send the customer to checkout_url; keep claim_token to complete it.
 */
export type CreateConsumerSignupResponse = {
  id: string;
  /**
   * Stripe Checkout URL; empty for the free plan, which completes at once.
   */
  checkout_url: string;
  claim_token: string;
  expires_at: string;
};

