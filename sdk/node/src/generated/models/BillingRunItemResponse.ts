/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What a billing run did for one app consumer or platform tenant.
 */
export type BillingRunItemResponse = {
  source_kind: 'app_consumer' | 'platform_tenant';
  app_id?: string;
  consumer_id?: string;
  platform_tenant_id?: string;
  /**
   * The consumer's or tenant's external_ref when the run started.
   */
  external_ref: string;
  /**
   * up_to_date: nothing new to bill or export. held: needs attention, such as unpriced usage or a completeness gap. skipped: billed through a platform tenant, or already invoiced through the other path.
   */
  outcome: 'pending' | 'drafted' | 'finalized' | 'exported' | 'up_to_date' | 'held' | 'skipped' | 'failed';
  statement_id?: string;
  revision?: number;
  currency?: string;
  /**
   * The statement revision's amount in millicents.
   */
  amount_millicents?: number;
  completeness?: 'verified' | 'partial' | 'gaps_detected' | 'unverifiable';
  /**
   * The last provider invoice this run created for the customer.
   */
  invoice_id?: string;
  /**
   * Provider invoices this run created for the customer.
   */
  exported_invoices: number;
  /**
   * Why the customer was held, skipped or failed, or why a retry is pending.
   */
  reason?: string;
  attempts: number;
  updated_at: string;
  /**
   * Payment status of the invoice exported for the item's statement (ADR-1504).
   */
  payment_status?: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted';
};

