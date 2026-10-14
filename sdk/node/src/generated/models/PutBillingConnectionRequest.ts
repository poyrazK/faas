/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The account's own key for one billing provider (ADR-1300, ADR-1305).
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
};

