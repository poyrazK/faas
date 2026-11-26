/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingProvider } from './BillingProvider.js';
/**
 * Provider invoice status as recorded by the account's payment sync.
 */
export type APIConsumerBillingInvoiceProgress = {
  id: string;
  provider: BillingProvider;
  livemode: boolean;
  payment_status?: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted';
  sent_at?: string;
  /**
   * Provider timestamp for a confirmed positive payment on this invoice.
   */
  paid_at?: string;
};

