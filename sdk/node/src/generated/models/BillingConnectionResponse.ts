/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingProvider } from './BillingProvider.js';
/**
 * A stored payment-provider key, identified without revealing it.
 */
export type BillingConnectionResponse = {
  provider: BillingProvider;
  key_last4: string;
  /**
   * False for Stripe test and Paddle sandbox keys; Lago has no test mode.
   */
  livemode: boolean;
  /**
   * The Lago API origin.
   */
  api_url?: string;
  created_at: string;
  updated_at: string;
};

