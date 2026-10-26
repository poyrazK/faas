/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Returned by POST /v1/billing/retry (issue #242). The CLI
 * prints attempt_id and provider_ref_id so the customer can
 * quote them to support if the charge still fails. status is
 * "pending_provider_confirmation" — the CLI does not poll for
 * a settlement flip; that flip arrives via the provider's
 * webhook (EventPaymentSucceeded) and is rendered by the
 * dashboard / dunning email pipeline.
 *
 */
export type BillingRetryResponse = {
  /**
   * apId-side attempt handle (Stripe: in_…, Paddle: txn_…-related).
   */
  attempt_id: string;
  /**
   * Provider-side handle for the new attempt (Stripe: pi_…, Paddle: tx_…).
   */
  provider_ref_id: string;
  /**
   * Always "pending_provider_confirmation" today; reserved for future settlement states.
   */
  status: string;
  /**
   * Next scheduled billing instant after the retry settles; null when not known yet.
   */
  next_billing_at?: string | null;
};

