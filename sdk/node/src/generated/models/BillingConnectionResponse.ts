/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A stored payment-provider key, identified without revealing it.
 */
export type BillingConnectionResponse = {
  provider: 'stripe';
  key_last4: string;
  livemode: boolean;
  created_at: string;
  updated_at: string;
};

