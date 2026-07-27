/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * RFC 7807 problem+json envelope. The `code` field is the stable
 * machine-readable identifier; clients branch on it. `limit` and
 * `observed` are populated on quota errors. `docs_url` points the
 * user at the next action. `billing_portal_url` is populated on
 * `code: payment_required` so the dashboard can deep-link the
 * customer to the Stripe-hosted billing portal (issue #142).
 * `paddle_checkout_url` + `tx_id` are populated instead when the
 * box is running on the Paddle billing provider
 * (`FAAS_BILLING_PROVIDER=paddle`, ADR-025) — the customer lands
 * on a Paddle-hosted checkout page for the target plan and the
 * dashboard renders the transaction handle as a confirmation id.
 * Exactly one of `billing_portal_url` or `paddle_checkout_url` is
 * populated on a given 402 — never both.
 *
 */
export type Problem = {
  type?: string;
  title: string;
  status: number;
  /**
   * Stable machine-readable error code. See StatusForCode in pkg/api/errors.go.
   */
  code: string;
  detail?: string;
  limit?: number | null;
  observed?: number | null;
  docs_url?: string;
  billing_portal_url?: string;
  /**
   * Paddle-hosted checkout URL on a `payment_required` 402 when
   * the box is running on the Paddle billing provider. Mutually
   * exclusive with `billing_portal_url`.
   *
   */
  paddle_checkout_url?: string;
  /**
   * Paddle transaction handle (`txn_…`) on a `payment_required`
   * 402. Empty on the Stripe path. The dashboard renders this as
   * a confirmation id after the customer completes checkout.
   *
   */
  tx_id?: string;
};

