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
  currency: 'USD';
  meters: Array<FinancialMeterCosts>;
  /**
   * Plan fee plus every meter's projected usage charge for the month, in millicents. Present only when every meter has an available forecast; it excludes the components listed in missing_bill_components.
   */
  projected_subtotal_millicents?: number;
  bill_estimate_available: boolean;
  missing_bill_components: Array<string>;
};

