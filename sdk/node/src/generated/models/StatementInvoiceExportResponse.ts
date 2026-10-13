/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A finalized statement's draft invoice in the account's Stripe (ADR-1300).
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
  source_kind?: 'app_consumer' | 'platform_tenant';
  /**
   * The Stripe invoice's status as last read by Gregale (ADR-1302); deleted means Stripe no longer has the draft. Absent until the first sync.
   */
  payment_status?: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted';
  amount_paid_minor?: number;
  amount_remaining_minor?: number;
  due_at?: string;
  paid_at?: string;
  /**
   * Stripe's customer-facing invoice page once the invoice is finalized.
   */
  hosted_invoice_url?: string;
  /**
   * Open, past its due date, with an amount remaining.
   */
  overdue?: boolean;
  status_synced_at?: string;
};

