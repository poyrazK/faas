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
  finalized_at?: string;
  invoice?: ConsumerBillingInvoice;
};

