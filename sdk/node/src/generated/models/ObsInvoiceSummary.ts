/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One invoice - number, status, and total; no line items.
 */
export type ObsInvoiceSummary = {
  id: string;
  provider: string;
  number?: string;
  status: string;
  currency: string;
  period_start: string;
  period_end: string;
  total_cents: number;
  amount_paid_cents: number;
};

