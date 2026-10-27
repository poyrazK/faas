/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerBillingInvoice } from './ConsumerBillingInvoice.js';
/**
 * One finalized statement as its consumer sees it.
 */
export type ConsumerBillingStatement = {
  id: string;
  period_start: string;
  period_end: string;
  revision: number;
  currency: string;
  billable_units: number;
  amount_millicents: number;
  /**
   * Prepaid credit drawn when the statement was finalized.
   */
  credit_applied_millicents: number;
  /**
   * amount_millicents less credit_applied_millicents: what is left to pay.
   */
  amount_due_millicents: number;
  finalized_at?: string;
  invoice?: ConsumerBillingInvoice;
};

