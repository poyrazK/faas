/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumedCreditRow } from './ConsumedCreditRow.js';
/**
 * Returned by POST /v1/invoices/{id}/consume-credits. consumed_cents is the floored integer cents of
 * overage drained against this invoice. remaining_credits_cents
 * is the sum of cents_remaining across the account's active
 * credits after the call. already_consumed_for_invoice is true
 * on idempotent replays (the reducer returns the same
 * consumed_cents without double-decrementing). per_credit lists
 * FIFO-ordered credit drains with their post-decrement balance.
 * Money is integer cents (CLAUDE.md).
 *
 */
export type ConsumeInvoiceResponse = {
  invoice_id: string;
  consumed_cents: number;
  remaining_credits_cents: number;
  already_consumed_for_invoice: boolean;
  per_credit: Array<ConsumedCreditRow>;
};

