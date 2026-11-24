/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The saved purchase terms, current checkout state and verified credit receipt.
 */
export type ConsumerCreditPurchaseResponse = {
  id: string;
  pack_id: string;
  currency: string;
  amount_millicents: number;
  status: 'pending' | 'credited' | 'expired';
  expires_at: string;
  /**
   * Stripe Checkout URL, returned when starting or resuming an open session.
   */
  checkout_url?: string;
  /**
   * The prepaid credit grant once Stripe payment is confirmed.
   */
  credit_grant_id?: string;
};

