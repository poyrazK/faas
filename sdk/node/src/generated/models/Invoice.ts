/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One persisted billing-provider invoice (issue #259). Money is
 * integer cents in the provider's currency; EUR is the platform
 * currency today, the field is preserved per-row so multi-currency
 * support can land without a backfill. The PDF availability flag
 * is the only PDF surface we expose — the hosted PDF URL is
 * provider-scoped and the customer fetches it from the
 * Stripe/Paddle portal, not via this API. The hosted URL itself
 * is not on the wire; the column exists in invoices.hosted_url
 * for PR-B audit only.
 *
 */
export type Invoice = {
  id: string;
  provider: 'stripe' | 'paddle';
  provider_invoice_id: string;
  number?: string;
  status: 'draft' | 'open' | 'paid' | 'uncollectible' | 'void';
  period_start: string;
  period_end: string;
  subtotal_cents: number;
  tax_cents: number;
  total_cents: number;
  amount_paid_cents: number;
  currency: 'eur';
  pdf_available: boolean;
  created_at: string;
};

