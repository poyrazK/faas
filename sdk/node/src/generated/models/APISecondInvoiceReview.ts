/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Next-cycle statement and billing status for an app consumer with a confirmed first payment.
 */
export type APISecondInvoiceReview = {
  consumer_id: string;
  /**
   * App customer display name.
   */
  name: string;
  /**
   * UTC month of the selected statement.
   */
  month: string;
  statement_id: string;
  status: 'usage_hold' | 'statement_draft' | 'ready_to_export' | 'stripe_draft' | 'awaiting_payment' | 'paid' | 'test_invoice' | 'review_invoice';
  /**
   * Completed draft run with the active usage hold.
   */
  hold_run_id?: string;
};

