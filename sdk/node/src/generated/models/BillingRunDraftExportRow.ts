/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StatementInvoiceExportResponse } from './StatementInvoiceExportResponse.js';
/**
 * Live review of one billing run item for Stripe draft export.
 */
export type BillingRunDraftExportRow = {
  id: string;
  consumer_id?: string;
  customer_name: string;
  stripe_customer?: string;
  statement_id?: string;
  revision?: number;
  currency?: string;
  /**
   * Projected amount due after credit, before provider tax and rounding.
   */
  amount_due_millicents?: number;
  status: 'ready' | 'blocked' | 'exported';
  reason?: string;
  blocker_code?: 'stripe_customer_missing';
  invoice?: StatementInvoiceExportResponse;
};

