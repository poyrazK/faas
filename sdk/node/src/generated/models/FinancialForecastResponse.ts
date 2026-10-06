/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialMeterCosts } from './FinancialMeterCosts.js';
/**
 * Meter projections; a complete bill forecast requires all bill components.
 */
export type FinancialForecastResponse = {
  period_start: string;
  period_end: string;
  as_of: string;
  currency: 'EUR';
  meters: Array<FinancialMeterCosts>;
  bill_estimate_available: boolean;
  missing_bill_components: Array<string>;
};

