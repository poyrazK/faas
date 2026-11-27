/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The Stripe customer and original draft review being linked.
 */
export type LinkBillingRunStripeCustomerRequest = {
  stripe_customer: string;
  statement_id: string;
  livemode: boolean;
  connection_updated_at: string;
};

