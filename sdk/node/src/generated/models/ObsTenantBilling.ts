/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsInvoiceSummary } from './ObsInvoiceSummary.js';
/**
 * Bounded billing summary - plan, status, and invoice totals only.
 */
export type ObsTenantBilling = {
  current_month_overage_cents: number;
  overage_cap_cents?: number | null;
  active_credits_cents: number;
  invoices: Array<ObsInvoiceSummary>;
};

