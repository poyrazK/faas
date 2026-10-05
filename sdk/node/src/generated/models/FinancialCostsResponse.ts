/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialMeterCosts } from './FinancialMeterCosts.js';
import type { Invoice } from './Invoice.js';
/**
 * Account usage costs for a UTC period; invoice facts remain separate.
 */
export type FinancialCostsResponse = {
  account_id: string;
  currency: 'EUR';
  period_start: string;
  period_end: string;
  as_of: string;
  retained_from: string;
  evidence_through_id: number;
  known_usage_millicents: number;
  meters: Array<FinancialMeterCosts>;
  scope: string;
  invoices: Array<Invoice>;
  invoice_reconciliation: 'not_reconciled';
  missing_bill_components: Array<string>;
};

