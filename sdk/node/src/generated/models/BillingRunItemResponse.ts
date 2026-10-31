/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What a billing run did for one app consumer, platform tenant or Product Control customer. A product_control item names its app in app_id and its customer in platform_tenant_id; its statement_id is the Product Control statement document.
 */
export type BillingRunItemResponse = {
  source_kind: 'app_consumer' | 'platform_tenant' | 'product_control';
  app_id?: string;
  consumer_id?: string;
  platform_tenant_id?: string;
  /**
   * The consumer's or tenant's external_ref when the run started.
   */
  external_ref: string;
  /**
   * up_to_date: nothing new to bill or export. held: needs attention, such as unpriced usage, a completeness gap, or Product Control runs with no recorded outcome. skipped: billed through a platform tenant, or already invoiced through the other path or by hand.
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
   * Payment status of the invoice exported for the item's statement.
   */
  payment_status?: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted';
};

