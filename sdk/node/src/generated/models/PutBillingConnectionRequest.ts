/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The account's own key for one billing provider (ADR-1502, ADR-1507).
 */
export type PutBillingConnectionRequest = {
  /**
   * A Stripe secret or restricted key (sk_… or rk_…), a Paddle API key (pdl_…), or a Lago API key.
   */
  secret_key: string;
  /**
   * Lago only: the API origin for the EU region or a self-hosted Lago, https:// only; default https://api.getlago.com.
   */
  api_url?: string;
  /**
   * What the provider does with exported invoices (ADR-1311): none leaves drafts (Lago issues them without charging); send has Stripe email the invoice; charge has Stripe charge the customer's default payment method, or Lago charge through its payment provider. Paddle supports none only.
   */
  collection?: 'none' | 'send' | 'charge';
  /**
   * With collection send, days until a sent invoice is due.
   */
  days_until_due?: number;
  /**
   * Stripe only: have Stripe Tax calculate tax on exported invoices (ADR-1313). Needs Stripe Tax and customer addresses.
   */
  automatic_tax?: boolean;
};

