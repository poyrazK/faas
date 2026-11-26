/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recorded Stripe payments and balances for one currency and mode in the run.
 */
export type BillingRunCollectionTotal = {
  currency: string;
  livemode: boolean;
  amount_paid_minor: number;
  amount_outstanding_minor: number;
  amount_overdue_minor: number;
  /**
   * Issued or sent invoices with an unconfirmed balance.
   */
  awaiting_check: number;
  /**
   * Invoices with a positive recorded payment or balance whose due date has passed.
   */
  invoices_due: number;
  paid_by_due_date: number;
};

