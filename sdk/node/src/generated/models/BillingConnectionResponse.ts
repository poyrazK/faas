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
  /**
   * What the provider does with exported invoices.
   */
  collection?: 'none' | 'send' | 'charge';
  /**
   * Days until a sent invoice is due.
   */
  days_until_due?: number;
  /**
   * Whether Stripe Tax calculates tax on the connection's exports.
   */
  automatic_tax?: boolean;
  created_at: string;
  updated_at: string;
};

