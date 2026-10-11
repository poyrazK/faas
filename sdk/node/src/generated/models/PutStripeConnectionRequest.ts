/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The account's own Stripe key for statement invoice exports (ADR-976).
 */
export type PutStripeConnectionRequest = {
  /**
   * A Stripe secret or restricted key (sk_… or rk_…); prefer a restricted key limited to invoices and invoice items.
   */
  secret_key: string;
};

