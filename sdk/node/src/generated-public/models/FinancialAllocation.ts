/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialAttribution } from './FinancialAttribution.js';
/**
 * Exact quantity-share allocation; row amounts and account totals reconcile.
 */
export type FinancialAllocation = {
  attribution: FinancialAttribution;
  quantity: number;
  gross_millicents: number;
  allowance_millicents: number;
  net_millicents: number;
};

