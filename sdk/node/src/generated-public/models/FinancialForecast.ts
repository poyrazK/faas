/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Quantity run-rate projection; absent amounts mean unavailable, never zero.
 */
export type FinancialForecast = {
  method: string;
  available: boolean;
  reason?: string;
  account_id: string;
  period_start: string;
  period_end: string;
  complete_through: string;
  price_version: string;
  meter: string;
  currency: 'USD';
  projected_quantity?: number;
  projected_net_millicents?: number;
  /**
   * Set only for account-level meters priced by more than one rate, such as object_storage (ADR-20261020160000).
   */
  projected_gross_millicents?: number;
};

