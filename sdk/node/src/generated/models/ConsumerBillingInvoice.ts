/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The customer-facing view of a statement's exported invoice.
 */
export type ConsumerBillingInvoice = {
  payment_status?: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted';
  amount_minor: number;
  amount_remaining_minor?: number;
  due_at?: string;
  paid_at?: string;
  /**
   * Stripe's page where the customer views and pays the invoice.
   */
  hosted_invoice_url?: string;
};

