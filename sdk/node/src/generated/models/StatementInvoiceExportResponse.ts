/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A finalized statement's draft invoice in the account's Stripe (ADR-976).
 */
export type StatementInvoiceExportResponse = {
  statement_id: string;
  provider: 'stripe';
  customer: string;
  /**
   * creating means an earlier attempt stopped part-way; repeat the export to resume it.
   */
  status: 'creating' | 'completed';
  invoice_id?: string;
  dashboard_url?: string;
  currency: string;
  /**
   * Invoice total in the currency's minor unit (cents): the statement total rounded to the cent.
   */
  amount_minor: number;
  livemode: boolean;
  created_at: string;
  completed_at?: string;
};

