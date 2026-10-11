/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialAppMeterCosts } from './FinancialAppMeterCosts.js';
/**
 * Retained usage costs directly attributed to one application; invoice and forecast amounts remain account scoped.
 */
export type FinancialAppCostsResponse = {
  app_id: string;
  app_slug: string;
  currency: 'EUR';
  period_start: string;
  period_end: string;
  as_of: string;
  known_usage_millicents: number;
  meters: Array<FinancialAppMeterCosts>;
  scope: string;
  scope_description: string;
  bill_estimate_available: boolean;
  missing_bill_components: Array<string>;
};

